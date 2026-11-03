FROM golang:1.26-bookworm AS backend-build
WORKDIR /src/backend_v2
COPY backend_v2/ ./
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/app

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=backend-build /out/server ./server
USER 10001:10001
EXPOSE 8080
CMD ["/app/server"]
