FROM php:8.4-cli

# curl, json and mbstring (the only extensions dapr/php-sdk requires) are already
# built into the official php image. Composer falls back to the unzip binary
# installed below when ext-zip is absent, so no extension install step is needed.
RUN apt-get update && apt-get install -y wget git unzip && apt-get clean
EXPOSE 3000
RUN mkdir -p /app
WORKDIR /app
COPY --from=composer:latest /usr/bin/composer /usr/bin/composer
COPY composer.json /app/composer.json
RUN composer install --no-dev -o -n

RUN mv "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini"
ENTRYPOINT ["php"]
CMD ["-S", "0.0.0.0:3000", "-t", "/app/src"]
ENV PHP_CLI_SERVER_WORKERS=50
COPY src/ src/
