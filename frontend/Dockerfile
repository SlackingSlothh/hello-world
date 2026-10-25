FROM node:24-bookworm-slim AS frontend-build
WORKDIR /src
ARG PUBLIC_API_BASE_URL=http://localhost:8080
ENV PUBLIC_API_BASE_URL=${PUBLIC_API_BASE_URL}
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . ./
RUN npm run build
RUN npm prune --omit=dev --no-audit --no-fund

FROM node:24-bookworm-slim AS frontend-runtime
WORKDIR /app
ENV NODE_ENV=production HOST=0.0.0.0 PORT=4321
COPY --from=frontend-build /src/node_modules ./node_modules
COPY --from=frontend-build /src/dist ./dist
EXPOSE 4321
CMD ["node", "./dist/server/entry.mjs"]
