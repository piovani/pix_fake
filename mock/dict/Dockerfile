# The DICT mock as a container: the server and the seed command, with the seed data loaded at start.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/dict ./cmd/dict \
 && CGO_ENABLED=0 go build -trimpath -o /out/seed ./cmd/seed

FROM alpine:3
RUN adduser -D -u 10001 mock
WORKDIR /app
COPY --from=build /out/dict /out/seed ./
USER mock
EXPOSE 8080
# The seed is idempotent, so it is safe to run it on every start; the server applies the migrations itself.
CMD ["sh", "-c", "./seed && exec ./dict"]
