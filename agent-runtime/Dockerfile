FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -o /bin/runtime ./cmd/runtime

FROM alpine:3.20
COPY --from=build /bin/runtime /runtime
COPY roles /roles
CMD ["/runtime"]
