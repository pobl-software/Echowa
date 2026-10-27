# Production image: builds the frontend and the Go binary, then ships only
# what the app needs at runtime. No .env is baked in. Configure with
# environment variables (APP_KEY, DB_DATABASE, ...), which Goravel reads
# directly. See ADR 0011.

# 1. Frontend assets -> public/build
FROM node:24-alpine AS assets
WORKDIR /build
COPY package.json package-lock.json ./
RUN npm ci
COPY vite.config.ts tsconfig.json components.json ./
COPY resources ./resources
RUN npm run build

# 2. Go binary, with the frontend and root template embedded (as make release)
FROM golang:1.27-alpine AS app
WORKDIR /build
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=assets /build/public/build ./public/build
COPY --from=assets /build/dist/npm-licenses.json ./dist/
RUN go run ./scripts/licenses && go build -tags release -trimpath -ldflags "-s -w" -o main .

# 3. Runtime
FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 app
WORKDIR /www

COPY --from=app /build/main ./main
# Sessions (file driver) and logs are written to storage/, the SQLite
# database to /data (mount a volume there).
RUN mkdir -p storage/framework/sessions storage/logs /data && chown -R app:app storage /data

USER app
ENV APP_ENV=production \
    APP_DEBUG=false \
    APP_HOST=0.0.0.0 \
    APP_PORT=3000 \
    DB_DATABASE=/data/echowa.sqlite
EXPOSE 3000

ENTRYPOINT ["/www/main"]
