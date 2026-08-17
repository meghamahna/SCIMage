# Build the server binary.
FROM golang:1.26.6 AS build

WORKDIR /src

# Download modules first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off gives a static binary that runs on the tiny base below.
# -s -w drop the symbol table and debug info to keep the image small.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/scimage ./cmd/server

# Run it on a minimal, non-root base. distroless static has no shell and no
# package manager, so there is very little for an attacker to use, and it ships
# the CA certificates the server needs for outbound HTTPS (webhooks, ARIA).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/scimage /usr/local/bin/scimage

# Tracks the default listen port. Override SCIM_ADDR to change where the server
# binds; this line is documentation only and does not affect binding.
EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["scimage"]
