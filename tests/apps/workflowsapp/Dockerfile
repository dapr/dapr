FROM mcr.microsoft.com/dotnet/aspnet:6.0 AS base
WORKDIR /app
EXPOSE 3000

FROM mcr.microsoft.com/dotnet/sdk:6.0 AS build
WORKDIR /src
COPY ["WorkflowActor.csproj", "./"]
RUN dotnet restore "WorkflowActor.csproj"
COPY . .
WORKDIR "/src/."
RUN dotnet build "WorkflowActor.csproj" -c Release -o /app/build

FROM build AS publish
RUN dotnet publish "WorkflowActor.csproj" -c Release -o /app/publish

FROM base AS final
WORKDIR /app
COPY --from=publish /app/publish .
ENTRYPOINT ["dotnet", "WorkflowActor.dll"]

# Make sure the app binds to port 3000
ENV ASPNETCORE_URLS http://*:3000