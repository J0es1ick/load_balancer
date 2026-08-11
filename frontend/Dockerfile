FROM node:22.23.1-alpine3.23 AS builder

WORKDIR /app
ARG VITE_PUBLIC_URL=http://localhost:8080/
ENV VITE_PUBLIC_URL=${VITE_PUBLIC_URL}
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build:live

FROM nginx:1.28.3-alpine3.23

RUN mkdir -p /etc/nginx/templates \
    && rm -f /etc/nginx/conf.d/default.conf \
    && chown -R nginx:nginx /etc/nginx/conf.d /etc/nginx/templates /var/cache/nginx /var/run
COPY --chown=nginx:nginx nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --chown=nginx:nginx 10-admin-token.envsh /docker-entrypoint.d/10-admin-token.envsh
COPY --from=builder --chown=nginx:nginx /app/dist /usr/share/nginx/html

USER nginx
EXPOSE 8080
