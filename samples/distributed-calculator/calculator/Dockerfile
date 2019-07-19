FROM node:8-alpine
WORKDIR /usr/src/app
COPY . .
RUN npm install
RUN npm run buildclient
EXPOSE 8080
EXPOSE 3000
CMD [ "npm", "run", "start" ]