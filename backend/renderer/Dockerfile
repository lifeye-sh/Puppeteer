FROM node:24.13.0-alpine@sha256:cd6fb7efa6490f039f3471a189214d5f548c11df1ff9e5b181aa49e22c14383e AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY replay.js ./
RUN npm run build && npm prune --omit=dev
FROM node:24.13.0-alpine@sha256:cd6fb7efa6490f039f3471a189214d5f548c11df1ff9e5b181aa49e22c14383e
RUN apk add --no-cache chromium ffmpeg font-noto-cjk font-dejavu && addgroup -g 10001 renderer && adduser -D -u 10001 -G renderer renderer
WORKDIR /app
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/public ./public
COPY *.mjs ./
USER 10001:10001
ENV HOME=/tmp
EXPOSE 8090
CMD ["node","server.mjs"]

