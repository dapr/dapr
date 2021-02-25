FROM php:8-cli

COPY --from=mlocati/php-extension-installer /usr/bin/install-php-extensions /usr/local/bin/
RUN apt-get update && apt-get install -y wget git unzip && apt-get clean
RUN install-php-extensions curl zip
EXPOSE 3000
RUN mkdir -p /app
WORKDIR /app
COPY --from=composer:latest /usr/bin/composer /usr/bin/composer
COPY composer.json /app/composer.json
COPY composer.lock /app/composer.lock
RUN composer install --no-dev -o -n

RUN mv "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini"
ENTRYPOINT ["php"]
CMD ["-S", "0.0.0.0:3000", "-t", "/app/src"]
ENV PHP_CLI_SERVER_WORKERS=50
COPY src/ src/
