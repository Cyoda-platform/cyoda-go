FROM golang:1.26 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /cyoda-go ./cmd/cyoda-go

FROM gcr.io/distroless/static

COPY --from=builder /cyoda-go /cyoda-go
COPY --from=builder --chown=9000:9000 /tmp /tmp

USER 9000

EXPOSE 8080 9090
ENTRYPOINT ["/cyoda-go"]
