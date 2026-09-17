FROM node:22.23.1-alpine3.23@sha256:8516dce0483394d5708d4b2ee6cacb79fb1d617ea4e2787c2120bcca92ce372e AS builder

WORKDIR /app
ARG VITE_PUBLIC_URL=http://localhost:8080/
ENV VITE_PUBLIC_URL=${VITE_PUBLIC_URL}
ENV VITE_APP_MODE=live
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build:live

FROM nginx:1.30.4-alpine3.24-slim@sha256:77da26c31397bf6694b4bf93275f5b40b0b120ba1b8f114264b603e592c561d6

RUN mkdir -p /etc/nginx/templates \
    && rm -f /etc/nginx/conf.d/default.conf \
    && chown -R nginx:nginx /etc/nginx/conf.d /etc/nginx/templates /var/cache/nginx /var/run
COPY --chown=nginx:nginx nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --chown=nginx:nginx 10-admin-token.envsh /docker-entrypoint.d/10-admin-token.envsh
RUN chmod 0755 /docker-entrypoint.d/10-admin-token.envsh
COPY --from=builder --chown=nginx:nginx /app/dist /usr/share/nginx/html

USER nginx
EXPOSE 8080
