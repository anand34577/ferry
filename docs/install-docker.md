# Install with Docker

Requirements: [Docker](https://docs.docker.com/get-docker/) with Docker Compose 2.24 or newer (included in current Docker Desktop and Docker Engine).

The image `ghcr.io/anand34577/ferry` runs on x86-64 and ARM (including Raspberry Pi).

## Start Ferry

The quickest way is one command:

```bash
docker run -d --name ferry --restart unless-stopped -p 8080:8080 -v ferry-data:/data ghcr.io/anand34577/ferry:latest
```

Then open **http://localhost:8080** and create the administrator account.

For easier settings and updates, use Docker Compose instead:

1. Create a folder, for example `ferry`, and download [`compose.yaml`](https://raw.githubusercontent.com/anand34577/ferry/main/compose.yaml) into it:

   ```bash
   mkdir ferry && cd ferry
   curl -fsSLO https://raw.githubusercontent.com/anand34577/ferry/main/compose.yaml
   ```

2. Start Ferry:

   ```bash
   docker compose up -d
   ```

Ferry now starts automatically with Docker. Your data is kept in the Docker volume `ferry-data` and survives updates and restarts.

## Change settings

1. In the same folder, download the example settings as `.env`:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/anand34577/ferry/main/deployment/.env.example -o .env
   ```

2. Open `.env` in a text editor. Remove the `#` in front of a setting to use it, for example:

   ```ini
   FERRY_SITE_NAME=Family files
   FERRY_PUBLIC_URL=https://files.example.com
   FERRY_DEFAULT_USER_QUOTA=50GB
   ```

3. Apply the changes:

   ```bash
   docker compose up -d
   ```

All settings are described in [Configuration](configuration.md).

## Use a different port

Change the left side of the port mapping in `compose.yaml`, for example `"9000:8080"`, then run `docker compose up -d`. Ferry is then at `http://localhost:9000`.

## Use PostgreSQL instead of SQLite

SQLite (the default) needs no setup and suits individuals and small teams. For larger teams, use PostgreSQL:

1. Download [`compose.postgres.yaml`](https://raw.githubusercontent.com/anand34577/ferry/main/compose.postgres.yaml) into your folder.
2. Add a database password to `.env` (letters and digits only):

   ```ini
   POSTGRES_PASSWORD=choose-a-long-password
   ```

3. Start both containers:

   ```bash
   docker compose -f compose.postgres.yaml up -d
   ```

Choose the database before you add data; Ferry does not convert between them.

## Everyday commands

| Task | Command |
|---|---|
| View logs | `docker compose logs -f` |
| Stop | `docker compose down` |
| Start | `docker compose up -d` |
| Update to the latest version | `docker compose pull && docker compose up -d` |
| Run an admin command | `docker compose exec ferry ferry user list` |
| Reset a password | `docker compose exec ferry ferry user reset-password -email you@example.com -password 'new-password'` |

`docker compose down` keeps your data. Only `docker compose down -v` deletes it.

To stay on a specific version, replace `latest` in `compose.yaml` with a version such as `v1.0.0`.

## Build the image yourself

From a copy of this repository:

```bash
docker build -t ghcr.io/anand34577/ferry:latest .
docker compose up -d
```

## Next steps

- [Put Ferry online with HTTPS](https.md)
- [Backup and restore](administration.md#backup-and-restore)
