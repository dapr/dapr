FROM mcr.microsoft.com/dotnet/aspnet:3.1 AS base
WORKDIR /app
EXPOSE 3000

FROM mcr.microsoft.com/dotnet/sdk:3.1 AS build
WORKDIR /src
COPY ["CarActor.csproj", "./"]
RUN dotnet restore "CarActor.csproj"
COPY . .
WORKDIR "/src/."
RUN dotnet build "CarActor.csproj" -c Release -o /app/build

FROM build AS publish
RUN dotnet publish "CarActor.csproj" -c Release -o /app/publish

FROM base AS final
WORKDIR /app
COPY --from=publish /app/publish .
ENTRYPOINT ["dotnet", "CarActor.dll"]
