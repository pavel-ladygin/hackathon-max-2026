FROM node:22-alpine AS build

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . ./

ARG VITE_API_BASE_URL=/api/v1
ARG VITE_MAX_APP_URL=https://max.ru/t255_hakaton_max_bot
ARG VITE_YANDEX_MAPS_API_KEY
ARG VITE_TICKET_PROVIDER_ALLOWLIST=kudago.com,*.kudago.com,timepad.ru,*.timepad.ru
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
ENV VITE_MAX_APP_URL=${VITE_MAX_APP_URL}
ENV VITE_YANDEX_MAPS_API_KEY=${VITE_YANDEX_MAPS_API_KEY}
ENV VITE_TICKET_PROVIDER_ALLOWLIST=${VITE_TICKET_PROVIDER_ALLOWLIST}
RUN npm run build

FROM nginx:1.31.6-alpine3.24
RUN apk upgrade --no-cache
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -q -O /dev/null http://127.0.0.1/health || exit 1
