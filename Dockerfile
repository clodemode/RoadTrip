# Build a static binary, ship it on scratch. No runtime, no package manager,
# nothing to patch — which is the point: nobody is patching anything from a car.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off: modernc.org/sqlite is pure Go, so the binary is fully static.
# tzdata is embedded via `import _ "time/tzdata"` — scratch has no zoneinfo.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /roadtrip .
# Fly's hallpass (the `fly ssh` / `fly ssh sftp` server) verifies the cert and
# then resolves the login user through /etc/passwd. scratch has none, so every
# session dies with "unable to authenticate" even though the log says
# verified=true. A one-line root entry is enough for sftp — which is how the
# private overlay (/data/private.json) reaches the volume. No shell is added.
RUN printf 'root:x:0:0:root:/:/sbin/nologin\n' > /tmp/passwd && printf 'root:x:0:\n' > /tmp/group

FROM scratch
# Platform path standard: code under /app, persistent data under /data. The
# binary is the whole of the code, so /app holds exactly one file. The data
# dir is the volume: the database, the photos, the private overlay and the
# trip files (/data/trips) — none of which is ever in this image.
WORKDIR /app
COPY --from=build /roadtrip /app/roadtrip
# TLS roots, for outbound HTTPS if it is ever needed.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /tmp/passwd /etc/passwd
COPY --from=build /tmp/group /etc/group
ENV DATA_DIR=/data PORT=8080
EXPOSE 8080
ENTRYPOINT ["/app/roadtrip"]
