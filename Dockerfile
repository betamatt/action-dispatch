FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /action-dispatch ./cmd/action-dispatch

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /action-dispatch /action-dispatch
ENTRYPOINT ["/action-dispatch"]
