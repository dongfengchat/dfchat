package group

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound          = errors.New("group not found")
	ErrNotMember         = errors.New("not a group member")
	ErrAlreadyMember     = errors.New("already a member")
	ErrGroupFull         = errors.New("group is full")
	ErrInviteCodeInvalid = errors.New("invalid invite code")
	ErrIsOwner           = errors.New("owner cannot leave; transfer ownership or delete the group")
)

type Group struct {
	ID           int64     `json:"id,string"`
	Type         int16     `json:"type"`
	Name         string    `json:"name"`
	IconURL      string    `json:"iconUrl,omitempty"`
	Description  string    `json:"description,omitempty"`
	Announcement string    `json:"announcement,omitempty"`
	OwnerID      int64     `json:"ownerId,string"`
	MemberCount  int       `json:"memberCount"`
	MaxMembers   int       `json:"maxMembers"`
	IsPublic     bool      `json:"isPublic"`
	InviteCode   string    `json:"inviteCode"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Member struct {
	UserID    int64     `json:"userId,string"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	AvatarURL string    `json:"avatarUrl,omitempty"`
	Role      int16     `json:"role"`
	JoinedAt  time.Time `json:"joinedAt"`
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func generateInviteCode() (string, error) {
	b := make([]byte, 8) // 8 bytes -> 16 base32 chars (Crockford lowercase)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return enc, nil
}

// Create inserts a group and adds the owner as a member (role=owner).
func (r *Repo) Create(ctx context.Context, ownerID int64, name string) (*Group, error) {
	code, err := generateInviteCode()
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	g := &Group{}
	if err := tx.QueryRow(ctx,
		`INSERT INTO groups (name, owner_id, invite_code, member_count) VALUES ($1, $2, $3, 1)
		 RETURNING id, type, name, COALESCE(icon_url,''), COALESCE(description,''),
		           owner_id, member_count, max_members, is_public, invite_code, created_at, COALESCE(announcement, '')`,
		name, ownerID, code,
	).Scan(&g.ID, &g.Type, &g.Name, &g.IconURL, &g.Description,
		&g.OwnerID, &g.MemberCount, &g.MaxMembers, &g.IsPublic, &g.InviteCode, &g.CreatedAt, &g.Announcement); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 2)`,
		g.ID, ownerID); err != nil {
		return nil, err
	}

	// Eagerly create the conversation + seq + owner membership so that
	// conversation_members is the single source of truth for access.
	convID := GroupConvID(g.ID)
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversations (id, type) VALUES ($1, 2) ON CONFLICT (id) DO NOTHING`,
		convID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_seq (conversation_id, last_seq) VALUES ($1, 0) ON CONFLICT DO NOTHING`,
		convID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_members (conversation_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		convID, ownerID); err != nil {
		return nil, err
	}

	// Also create a default "general" text channel so the group is usable
	// immediately under the multi-channel model.
	var channelID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO channels (group_id, name, position) VALUES ($1, 'general', 0) RETURNING id`,
		g.ID).Scan(&channelID); err != nil {
		return nil, err
	}
	channelConvID := fmt.Sprintf("c_%d", channelID)
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversations (id, type) VALUES ($1, 2) ON CONFLICT DO NOTHING`,
		channelConvID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_seq (conversation_id, last_seq) VALUES ($1, 0) ON CONFLICT DO NOTHING`,
		channelConvID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_members (conversation_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		channelConvID, ownerID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return g, nil
}

// ListMine returns groups the user belongs to.
func (r *Repo) ListMine(ctx context.Context, userID int64) ([]*Group, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT g.id, g.type, g.name, COALESCE(g.icon_url,''), COALESCE(g.description,''),
		        g.owner_id, g.member_count, g.max_members, g.is_public, g.invite_code, g.created_at, COALESCE(g.announcement, '')
		 FROM groups g
		 JOIN group_members gm ON gm.group_id = g.id
		 WHERE gm.user_id = $1
		 ORDER BY g.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Group, 0)
	for rows.Next() {
		g := &Group{}
		if err := rows.Scan(&g.ID, &g.Type, &g.Name, &g.IconURL, &g.Description,
			&g.OwnerID, &g.MemberCount, &g.MaxMembers, &g.IsPublic, &g.InviteCode, &g.CreatedAt, &g.Announcement); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FindByID returns the group regardless of caller membership. Callers
// must validate access.
func (r *Repo) FindByID(ctx context.Context, id int64) (*Group, error) {
	g := &Group{}
	err := r.pool.QueryRow(ctx,
		`SELECT id, type, name, COALESCE(icon_url,''), COALESCE(description,''),
		        owner_id, member_count, max_members, is_public, invite_code, created_at, COALESCE(announcement, '')
		 FROM groups WHERE id = $1`, id,
	).Scan(&g.ID, &g.Type, &g.Name, &g.IconURL, &g.Description,
		&g.OwnerID, &g.MemberCount, &g.MaxMembers, &g.IsPublic, &g.InviteCode, &g.CreatedAt, &g.Announcement)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

// JoinByInvite adds userID to the group identified by inviteCode.
func (r *Repo) JoinByInvite(ctx context.Context, inviteCode string, userID int64) (*Group, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var (
		groupID     int64
		memberCount int
		maxMembers  int
	)
	if err := tx.QueryRow(ctx,
		`SELECT id, member_count, max_members FROM groups WHERE invite_code = $1 FOR UPDATE`,
		inviteCode).Scan(&groupID, &memberCount, &maxMembers); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInviteCodeInvalid
		}
		return nil, err
	}
	if memberCount >= maxMembers {
		return nil, ErrGroupFull
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)
		 ON CONFLICT (group_id, user_id) DO NOTHING`, groupID, userID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrAlreadyMember
	}
	if _, err := tx.Exec(ctx,
		`UPDATE groups SET member_count = member_count + 1 WHERE id = $1`, groupID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_members (conversation_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		GroupConvID(groupID), userID); err != nil {
		return nil, err
	}
	// Also grant access to every existing channel inside the group.
	if _, err := tx.Exec(ctx,
		`INSERT INTO conversation_members (conversation_id, user_id)
		 SELECT 'c_' || id::text, $1 FROM channels WHERE group_id = $2
		 ON CONFLICT DO NOTHING`, userID, groupID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.FindByID(ctx, groupID)
}

// Leave removes userID from the group. Owner cannot leave (must transfer or delete).
func (r *Repo) Leave(ctx context.Context, groupID, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var ownerID int64
	if err := tx.QueryRow(ctx, `SELECT owner_id FROM groups WHERE id = $1`, groupID).Scan(&ownerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if ownerID == userID {
		return ErrIsOwner
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotMember
	}
	if _, err := tx.Exec(ctx,
		`UPDATE groups SET member_count = member_count - 1 WHERE id = $1`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM conversation_members WHERE conversation_id = $1 AND user_id = $2`,
		GroupConvID(groupID), userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM conversation_members
		 WHERE user_id = $1 AND conversation_id IN
		   (SELECT 'c_' || id::text FROM channels WHERE group_id = $2)`,
		userID, groupID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Members returns all members of a group with their user info.
func (r *Repo) Members(ctx context.Context, groupID int64) ([]*Member, error) {
	return r.MembersFiltered(ctx, groupID, "")
}

// MembersFiltered returns the group's members, optionally filtered by
// a case-insensitive substring match on username OR nickname (group
// override, falling back to user nickname). Empty q returns every
// member, identical to the legacy Members() shape.
func (r *Repo) MembersFiltered(ctx context.Context, groupID int64, q string) ([]*Member, error) {
	const base = `
		SELECT gm.user_id, u.username, COALESCE(gm.nickname, u.nickname),
		       COALESCE(u.avatar_url,''), gm.role, gm.joined_at
		FROM group_members gm
		JOIN users u ON u.id = gm.user_id
		WHERE gm.group_id = $1`
	const order = ` ORDER BY gm.role DESC, gm.joined_at ASC`
	var (
		rows interface {
			Next() bool
			Scan(...any) error
			Close()
			Err() error
		}
		err error
	)
	if q == "" {
		rows, err = r.pool.Query(ctx, base+order, groupID)
	} else {
		// ILIKE pattern; quote any %/_ in the user input so substrings
		// are literal. Then surround with %…% for substring match.
		safe := strings.ReplaceAll(q, `\`, `\\`)
		safe = strings.ReplaceAll(safe, `%`, `\%`)
		safe = strings.ReplaceAll(safe, `_`, `\_`)
		rows, err = r.pool.Query(ctx, base+
			` AND (u.username ILIKE $2 OR COALESCE(gm.nickname, u.nickname) ILIKE $2)`+order,
			groupID, "%"+safe+"%")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Member, 0)
	for rows.Next() {
		m := &Member{}
		if err := rows.Scan(&m.UserID, &m.Username, &m.Nickname, &m.AvatarURL, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Delete hard-deletes a group. Caller MUST gate on owner role. The FK
// constraints on group_members / channels / conversation_members /
// group_notify_modes are all ON DELETE CASCADE, so a single DELETE
// here cleans up the entire shape. We don't touch the conversations /
// messages rows — those persist so historical exports stay sane, but
// nobody is in conversation_members anymore so they're unreachable.
func (r *Repo) Delete(ctx context.Context, groupID int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TransferOwnership demotes the current owner to admin and promotes the
// named target user to owner. Verifies the target is already in the
// group; returns ErrNotMember if not. Caller MUST gate on the caller
// being the current owner (we don't re-check here — handler does).
func (r *Repo) TransferOwnership(ctx context.Context, groupID, oldOwner, newOwner int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Make sure new owner is actually a member.
	var role int16
	if err := tx.QueryRow(ctx,
		`SELECT role FROM group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, newOwner).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotMember
		}
		return err
	}

	// owner_id on the group row
	if _, err := tx.Exec(ctx,
		`UPDATE groups SET owner_id = $1 WHERE id = $2`, newOwner, groupID); err != nil {
		return err
	}
	// new owner → role 2
	if _, err := tx.Exec(ctx,
		`UPDATE group_members SET role = 2 WHERE group_id = $1 AND user_id = $2`,
		groupID, newOwner); err != nil {
		return err
	}
	// old owner → demote to admin (1) so they keep enough power to
	// leave cleanly or assist the new owner.
	if _, err := tx.Exec(ctx,
		`UPDATE group_members SET role = 1 WHERE group_id = $1 AND user_id = $2`,
		groupID, oldOwner); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RotateInviteCode generates a fresh code and returns it. The old code
// becomes unusable. Used by /groups/:id/invite/rotate — also called
// implicitly inside Kick.
func (r *Repo) RotateInviteCode(ctx context.Context, groupID int64) (string, error) {
	code, err := generateInviteCode()
	if err != nil {
		return "", err
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE groups SET invite_code = $2 WHERE id = $1`, groupID, code)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return code, nil
}

// GetMemberRole returns 0/1/2 for member/admin/owner; ErrNotMember if absent.
func (r *Repo) GetMemberRole(ctx context.Context, groupID, userID int64) (int16, error) {
	var role int16
	err := r.pool.QueryRow(ctx,
		`SELECT role FROM group_members WHERE group_id=$1 AND user_id=$2`, groupID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotMember
	}
	return role, err
}

// SetMemberRole flips a member between 0:member and 1:admin. Refuses to
// touch role=2 (owner) — that requires a dedicated transfer flow.
func (r *Repo) SetMemberRole(ctx context.Context, groupID, userID int64, role int16) error {
	if role != 0 && role != 1 {
		return errors.New("role must be 0 or 1")
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE group_members SET role = $3 WHERE group_id=$1 AND user_id=$2 AND role != 2`,
		groupID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotMember
	}
	return nil
}

// Kick removes a non-owner member from the group and from every conversation
// (group + every channel) so they immediately lose access. Returns ErrIsOwner
// if the target is the group owner.
func (r *Repo) Kick(ctx context.Context, groupID, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var ownerID int64
	if err := tx.QueryRow(ctx, `SELECT owner_id FROM groups WHERE id = $1`, groupID).Scan(&ownerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if ownerID == userID {
		return ErrIsOwner
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM group_members WHERE group_id=$1 AND user_id=$2`, groupID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotMember
	}
	if _, err := tx.Exec(ctx, `UPDATE groups SET member_count = member_count - 1 WHERE id = $1`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM conversation_members WHERE conversation_id = $1 AND user_id = $2`,
		GroupConvID(groupID), userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM conversation_members
		 WHERE user_id = $1 AND conversation_id IN
		   (SELECT 'c_' || id::text FROM channels WHERE group_id = $2)`,
		userID, groupID); err != nil {
		return err
	}
	// Rotate the invite code so the kicked user can't immediately rejoin
	// with the same code they already know. Remaining members will need
	// to refetch the group detail to see the new code — a small UX hit,
	// but the alternative (kick-then-rejoin no-op) is worse.
	newCode, err := generateInviteCode()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE groups SET invite_code = $2 WHERE id = $1`, groupID, newCode); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsMember reports whether userID belongs to groupID.
// TransferOwnershipForLeavingUser scans every group owned by oldOwnerID
// and tries to keep the group alive after the owner's account is gone:
//
//   1. Promote the longest-tenured admin (role=1) to owner. Their
//      group_members.role goes to 2 and groups.owner_id is rewritten.
//   2. If no admin, promote the longest-tenured remaining member.
//   3. If no remaining member at all, mark the group as archived
//      (is_archived=TRUE) — it becomes a read-only memorial.
//
// Idempotent: subsequent calls on the same user do nothing. Designed to
// be invoked right before user.SoftDelete so the owner_id never dangles
// at a scrubbed account.
func (r *Repo) TransferOwnershipForLeavingUser(ctx context.Context, oldOwnerID int64) error {
	rows, err := r.pool.Query(ctx,
		`SELECT id FROM groups WHERE owner_id = $1`, oldOwnerID)
	if err != nil {
		return err
	}
	groupIDs := make([]int64, 0)
	for rows.Next() {
		var gid int64
		if err := rows.Scan(&gid); err != nil {
			rows.Close()
			return err
		}
		groupIDs = append(groupIDs, gid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, gid := range groupIDs {
		if err := r.transferOneGroup(ctx, gid, oldOwnerID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) transferOneGroup(ctx context.Context, groupID, oldOwnerID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Pick a new owner: prefer the oldest admin, fall back to oldest
	// regular member. NULL means "no other member" → archive.
	var heirID *int64
	err = tx.QueryRow(ctx, `
		SELECT user_id FROM group_members
		WHERE group_id = $1 AND user_id <> $2
		ORDER BY CASE WHEN role = 1 THEN 0 ELSE 1 END,
		         joined_at ASC, user_id ASC
		LIMIT 1`, groupID, oldOwnerID).Scan(&heirID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	if heirID == nil {
		// No survivor — archive and remove old owner row.
		if _, err := tx.Exec(ctx,
			`UPDATE groups SET is_archived = TRUE WHERE id = $1`, groupID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`,
			groupID, oldOwnerID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	// Promote heir, demote/remove old owner. owner_id flip + role flip.
	if _, err := tx.Exec(ctx,
		`UPDATE groups SET owner_id = $1 WHERE id = $2`, *heirID, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE group_members SET role = 2 WHERE group_id = $1 AND user_id = $2`,
		groupID, *heirID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, oldOwnerID); err != nil {
		return err
	}
	// Also drop the conv-member row so the leaving user loses access.
	if _, err := tx.Exec(ctx,
		`DELETE FROM conversation_members WHERE user_id = $1
		   AND (conversation_id = $2
		     OR conversation_id IN (SELECT 'c_'||id::text FROM channels WHERE group_id = $3))`,
		oldOwnerID, GroupConvID(groupID), groupID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SharesGroupWith reports whether two users belong to at least one
// group in common. Used by the WS relay backend to decide whether `a`
// is allowed to send WebRTC signaling or typing pings to `b` even if
// they aren't direct friends.
func (r *Repo) SharesGroupWith(ctx context.Context, a, b int64) (bool, error) {
	if a == b {
		return false, nil
	}
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT 1
		FROM group_members ga
		JOIN group_members gb ON ga.group_id = gb.group_id
		WHERE ga.user_id = $1 AND gb.user_id = $2
		LIMIT 1`, a, b).Scan(&n)
	if err != nil {
		// pgx.ErrNoRows or other — treat as no shared group.
		return false, nil
	}
	return n == 1, nil
}

func (r *Repo) IsMember(ctx context.Context, groupID, userID int64) (bool, error) {
	var ok int
	err := r.pool.QueryRow(ctx,
		`SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return ok == 1, err
}

// MemberIDs returns the user ids of all members of a group (for fan-out).
func (r *Repo) MemberIDs(ctx context.Context, groupID int64) ([]int64, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM group_members WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GroupConvID returns the canonical conversation id for a group.
func GroupConvID(groupID int64) string {
	return fmt.Sprintf("g_%d", groupID)
}

// UpdateInput is the patch payload for Update. nil = leave unchanged.
type UpdateInput struct {
	Name         *string
	IconURL      *string
	Description  *string
	Announcement *string
	IsPublic     *bool
}

// Update mutates editable fields. Caller must verify owner/admin permission.
func (r *Repo) Update(ctx context.Context, id int64, in UpdateInput) (*Group, error) {
	sets := make([]string, 0, 5)
	args := make([]any, 0, 6)
	idx := 1
	if in.Name != nil {
		sets = append(sets, "name = $"+itoa(idx))
		args = append(args, *in.Name)
		idx++
	}
	if in.IconURL != nil {
		sets = append(sets, "icon_url = NULLIF($"+itoa(idx)+", '')")
		args = append(args, *in.IconURL)
		idx++
	}
	if in.Description != nil {
		sets = append(sets, "description = NULLIF($"+itoa(idx)+", '')")
		args = append(args, *in.Description)
		idx++
	}
	if in.Announcement != nil {
		sets = append(sets, "announcement = NULLIF($"+itoa(idx)+", '')")
		args = append(args, *in.Announcement)
		idx++
	}
	if in.IsPublic != nil {
		sets = append(sets, "is_public = $"+itoa(idx))
		args = append(args, *in.IsPublic)
		idx++
	}
	if len(sets) == 0 {
		return r.FindByID(ctx, id)
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)
	q := "UPDATE groups SET " + joinComma(sets) +
		" WHERE id = $" + itoa(idx) +
		` RETURNING id, type, name, COALESCE(icon_url,''), COALESCE(description,''),
		           owner_id, member_count, max_members, is_public, invite_code, created_at, COALESCE(announcement, '')`
	g := &Group{}
	if err := r.pool.QueryRow(ctx, q, args...).Scan(
		&g.ID, &g.Type, &g.Name, &g.IconURL, &g.Description,
		&g.OwnerID, &g.MemberCount, &g.MaxMembers, &g.IsPublic, &g.InviteCode, &g.CreatedAt, &g.Announcement,
	); err != nil {
		return nil, err
	}
	return g, nil
}

// SetNotifyMode upserts the per-user notify preference for a group.
// 0=all, 1=mention-only, 2=muted.
func (r *Repo) SetNotifyMode(ctx context.Context, groupID, userID int64, mode int16) error {
	if mode < 0 || mode > 2 {
		return errors.New("invalid mode")
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO group_notify_modes (group_id, user_id, mode) VALUES ($1, $2, $3)
		 ON CONFLICT (group_id, user_id) DO UPDATE SET mode = EXCLUDED.mode, updated_at = NOW()`,
		groupID, userID, mode)
	return err
}

// GetNotifyMode returns 0 (all) if no row exists.
func (r *Repo) GetNotifyMode(ctx context.Context, groupID, userID int64) (int16, error) {
	var mode int16
	err := r.pool.QueryRow(ctx,
		`SELECT mode FROM group_notify_modes WHERE group_id = $1 AND user_id = $2`,
		groupID, userID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return mode, err
}

func itoa(i int) string {
	out := make([]byte, 0, 4)
	if i == 0 {
		return "0"
	}
	for i > 0 {
		out = append([]byte{byte('0' + i%10)}, out...)
		i /= 10
	}
	return string(out)
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
