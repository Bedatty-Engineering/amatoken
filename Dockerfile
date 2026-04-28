FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
# Copy branding assets into the embedded static dir so they ship inside the binary.
RUN cp assets/img/amatoken-logo.png internal/httpapi/static/logo.png \
 && cp assets/img/amatoken-banner.png internal/httpapi/static/banner.png
RUN go mod tidy
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/amatoken ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && mkdir -p /data && chmod 777 /data
COPY --from=build /out/amatoken /usr/local/bin/amatoken
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/amatoken"]
