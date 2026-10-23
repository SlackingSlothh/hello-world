FROM node:24-bookworm-slim AS frontend-build
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-bookworm AS backend-build
WORKDIR /src/backend
COPY backend/ ./
RUN CGO_ENABLED=0 go build -o /out/server .

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=backend-build /out/server ./server
COPY --from=frontend-build /src/frontend/dist ./frontend/dist
USER 10001:10001
EXPOSE 8080
CMD ["/app/server"]
