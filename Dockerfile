FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o gomail ./cmd/server

# ---- Runtime ----
FROM alpine:3.19
RUN apk add --no-cache sqlite-libs ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/gomail .
COPY --from=builder /app/web ./web

RUN mkdir -p /data && addgroup -S gomail && adduser -S gomail -G gomail
RUN chown -R gomail:gomail /app /data
USER gomail

VOLUME ["/data"]
EXPOSE 8080

ENV DB_PATH=/data/gomail.db
ENV LISTEN_ADDR=:8080

CMD ["./gomail"]
