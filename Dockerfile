FROM node:22-alpine AS web
WORKDIR /src
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run check && npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ailist .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 ailist
WORKDIR /app
COPY --from=build /ailist /app/ailist
COPY --from=web /src/dist /app/web/dist
USER ailist
EXPOSE 8080
ENTRYPOINT ["/app/ailist"]
