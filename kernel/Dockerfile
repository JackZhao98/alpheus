FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -o /bin/kernel ./cmd/kernel

FROM alpine:3.20
COPY --from=build /bin/kernel /kernel
COPY limits.yaml /limits.yaml
EXPOSE 8100
CMD ["/kernel"]
