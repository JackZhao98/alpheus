FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -mod=readonly -o /bin/kernel ./cmd/kernel
RUN CGO_ENABLED=0 go build -mod=readonly -o /bin/export-agent-secret ./cmd/export-agent-secret

FROM alpine:3.20
COPY --from=build /bin/kernel /kernel
COPY --from=build /bin/export-agent-secret /export-agent-secret
COPY limits.yaml /limits.yaml
EXPOSE 8100
CMD ["/kernel"]
