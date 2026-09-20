FROM node:22-alpine AS build

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . ./

ARG VITE_DISCOVERY_SOURCE=mock
ARG VITE_API_BASE_URL=/api/v1
ARG VITE_MAX_APP_URL=https://max.ru
ENV VITE_DISCOVERY_SOURCE=${VITE_DISCOVERY_SOURCE}
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
ENV VITE_MAX_APP_URL=${VITE_MAX_APP_URL}
RUN npm run build

FROM nginx:1.31.6-alpine3.24
RUN apk upgrade --no-cache
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -q -O /dev/null http://127.0.0.1/health || exit 1
