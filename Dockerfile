FROM golang:1.26.1-alpine AS builder
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
      -ldflags "-s -w" \
      -o kansou .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 1000 kansou \
    && adduser -D -u 1000 -G kansou kansou

WORKDIR /app
COPY --from=builder /build/kansou /app/kansou

USER kansou
EXPOSE 8080
ENTRYPOINT ["/app/kansou"]
CMD ["serve"]
