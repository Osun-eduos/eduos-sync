FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /eduos-sync ./cmd/eduos-sync

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /eduos-sync /eduos-sync
EXPOSE 4000
ENTRYPOINT ["/eduos-sync"]
