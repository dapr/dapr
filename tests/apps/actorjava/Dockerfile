# build stage build the jar with all our resources
FROM maven:3-eclipse-temurin-11 as build

VOLUME /tmp
WORKDIR /build

COPY pom.xml .
RUN mvn dependency:go-offline

ADD src/ /build/src/
RUN mvn package

# package stage
FROM eclipse-temurin:11-jre

ARG JAR_FILE
COPY --from=build /build/target/app.jar /opt/app.jar
WORKDIR /opt/

EXPOSE 3000
ENTRYPOINT java -jar app.jar --server.port=3000