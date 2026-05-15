package group

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
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
	rows, err := r.pool.Query(ctx,
		`SELECT gm.user_id, u.username, COALESCE(gm.nickname, u.nickname),
		        COALESCE(u.avatar_url,''), gm.role, gm.joined_at
		 FROM group_members gm
		 JOIN users u ON u.id = gm.user_id
		 WHERE gm.group_id = $1
		 ORDER BY gm.role DESC, gm.joined_at ASC`, groupID)
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
	return tx.Commit(ctx)
}

// IsMember reports whether userID belongs to groupID.
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
}

// Update mutates editable fields. Caller must verify owner/admin permission.
func (r *Repo) Update(ctx context.Context, id int64, in UpdateInput) (*Group, error) {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
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
