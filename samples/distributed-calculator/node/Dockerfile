FROM node:8-alpine
WORKDIR /usr/src/app
COPY . .
RUN npm install
EXPOSE 4000
CMD [ "node", "app.js" ]