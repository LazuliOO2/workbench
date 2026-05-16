FROM golang:1.23-alpine AS build

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN go build -o /app/lenovo-scraper ./cmd/api

FROM alpine:3.20

WORKDIR /app

COPY --from=build /app/lenovo-scraper /app/lenovo-scraper

EXPOSE 8080

CMD ["/app/lenovo-scraper"]
