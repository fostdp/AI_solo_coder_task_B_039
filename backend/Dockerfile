FROM golang:1.21-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git gcc musl-dev

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build -ldflags '-w -s -linkmode external -extldflags "-static"' \
    -o /app/lng-monitor .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder /app/lng-monitor /app/lng-monitor
COPY config/model_params.json /app/config/model_params.json
COPY .env.example /app/.env

EXPOSE 8080 6060

CMD ["/app/lng-monitor"]
