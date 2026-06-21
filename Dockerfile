# syntax=docker/dockerfile:1.7

# -------- build stage --------
# Alpine for musl, which we statically link against so the scratch image works.
FROM golang:1.26-alpine AS build

# build-base brings gcc + musl-dev + make, which mattn/go-sqlite3 needs to
# compile its embedded C sources.
RUN apk add --no-cache build-base ca-certificates

WORKDIR /src

# Cache module downloads in their own layer so source edits don't refetch.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is required (mattn/go-sqlite3). Static link via musl + ld -static so
# the binary has no runtime libc dependency and can run on FROM scratch.
#
# Build tags:
#   sqlite_omit_load_extension - drop dlopen path we don't use
#   osusergo,netgo             - pure-Go user/dns resolvers (no /etc/* needed
#                                at runtime, which scratch doesn't have)
ENV CGO_ENABLED=1 GOOS=linux
RUN go build \
    -tags 'sqlite_omit_load_extension osusergo netgo' \
    -ldflags '-s -w -linkmode external -extldflags "-static"' \
    -trimpath \
    -o /out/nesco-monitor .

# -------- runtime stage --------
FROM scratch

# HTTPS to discord.com and customer.nesco.gov.bd needs trusted roots.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# tzdata is embedded via `_ "time/tzdata"`, so no /usr/share/zoneinfo COPY.

# /data is the conventional volume mount point for the SQLite file.
# Set DB_PATH=/data/nesco.db at runtime and mount a volume there for
# persistence across container restarts.
COPY --from=build /out/nesco-monitor /nesco-monitor

ENTRYPOINT ["/nesco-monitor"]
