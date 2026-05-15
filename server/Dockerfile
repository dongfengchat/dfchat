# Multi-stage build for the DFCHAT API server.
#
# Stage 1 compiles a static binary; stage 2 ships only that binary on a
# distroless base so the runtime image is ~15 MB with no shell / package
# manager / CVE-surface.

FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module fetch on its own layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags='-s -w' \
    -o /out/api ./cmd/api

# ---- runtime ---------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
