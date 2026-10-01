FROM golang:1.22 AS build
WORKDIR /src
COPY api/go.mod ./
RUN go mod download
COPY api/*.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /api .

FROM scratch
COPY --from=build /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
