# slack-site

Turn a **Slack workspace export** into a **local SQLite database**, a **Bleve search index**, and an optional **mirror** of message file attachments. Browse channels, DMs, groups, and MPIMs in a small web UI, with full-text search over message bodies.

## Features

- **Ingest** standard Slack export folders into `slack.db` (SQLite via [Bun](https://bun.uptrace.dev/)) and `slack.bleve` (full-text index).
- **Serve** a read-only site: home, channel/group/DM/MPIM lists, per-conversation timelines (with pagination), and search.
- **Rich text** in exports is rendered to HTML where possible; plain text is escaped.
- **Mirror files** from `url_private` to a local directory (`file://`) or **Amazon S3** (`s3://`), with re-entrant progress stored in the database.
- **Reindex** messages into a new Bleve index without re-running a full ingest.

## Requirements

- **Go** 1.24 or newer (see `go.mod`).
- A **Slack export** (JSON) from your workspace. Slack’s export format includes top-level files such as `users.json`, `channels.json`, and one directory per conversation containing daily `*.json` message files.
- For **downloading private files** during `mirror-files`, a **Slack API token** with access to those files (see [Environment variables](#environment-variables)).
- For **S3 mirroring**, AWS credentials (e.g. `aws configure` or SSO) and optionally the [AWS CLI](https://aws.amazon.com/cli/) for bucket setup.

## Build

From the repository root:

```bash
go build -o slack-site .
```

Or use Make (rebuilds when Go sources or embedded HTML templates change):

```bash
make build
```

The binary is named `slack-site` in the current directory.

## Quick start

1. Obtain a Slack export ZIP, unzip it, and note the path to the folder that contains `users.json` (and channel directories).

2. **Ingest** into a data directory (created if needed). This **overwrites** any existing `slack.db` and `slack.bleve` in that directory.

   ```bash
   ./slack-site ingest --input /path/to/slack/export --data ./data
   ```

3. **Serve** the site (opens your default browser on macOS, Windows, and typical Linux desktops):

   ```bash
   ./slack-site serve --data ./data
   ```

   By default the server listens on `:8080`. Open the printed URL (e.g. `http://127.0.0.1:8080`).

4. Optional: **mirror** attachments, then point the UI at your mirror with `--mirror` when serving (see [Mirroring files](#mirroring-files)).

## Slack export layout

The ingest step expects a directory structure like Slack’s export:

| Path | Role |
|------|------|
| `users.json` | Users |
| `channels.json`, `groups.json`, `dms.json`, `mpims.json` | Conversations and membership |
| `<conversation_id_or_name>/*.json` | Message history (per day or shard) |

Pass **`--input`** to the folder that **directly contains** `users.json` (not a parent that only holds the zip’s outer wrapper).

## Commands

### `ingest`

Reads the export and writes:

- **`slack.db`** — SQLite database with users, conversations, members, messages, attachments, and file metadata.
- **`slack.bleve`** — Bleve index for search (message text as stored after HTML rendering from blocks).

```text
slack-site ingest --input <export-dir> --data <data-dir>
```

| Flag | Required | Description |
|------|----------|-------------|
| `--input` | Yes | Path to the Slack export root (contains `users.json`). |
| `--data` | Yes | Directory where `slack.db` and `slack.bleve` are created. |

**Note:** Existing `slack.db` at `<data-dir>/slack.db` is removed and recreated. The Bleve directory under `<data-dir>` is replaced.

### `serve`

Serves the ingested data over HTTP using embedded HTML templates.

```text
slack-site serve --data <data-dir> [--addr <listen>] [--mirror <base-url>]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--data` | Yes | Directory that contains `slack.db` (same as **ingest** `--data`). |
| `--addr` | No | Listen address (default `:8080`). Examples: `localhost:3000`, `127.0.0.1:8080`. |
| `--mirror` | No | Base URL for message **file** links. When set, each file’s `url_private` is replaced by `base + '/' + relativePath`, where `relativePath` is derived from the URL and filename. Use this after mirroring files to a static host or CDN. **Do not** include a trailing slash (it is trimmed). |

**Search:** If `slack.bleve` is present next to `slack.db`, search works. If the index is missing, the search page still loads but returns no indexed results.

**Browser:** On start, the tool attempts to open the root URL via `open` (macOS), `xdg-open` (Linux), or `rundll32` (Windows).

### `mirror-files`

Downloads each distinct `url_private` from the `message_files` table and writes objects under a **mirror root**. Progress is recorded in the **`mirrored_files`** table so runs can be resumed.

```text
slack-site mirror-files --data <data-dir> --mirror <destination> [options]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--data` | Yes | Directory containing `slack.db`. |
| `--mirror` | Yes | Destination: `file:///absolute/path/to/dir` or `s3://bucket/prefix`. Trailing slashes are normalized. |
| `--concurrency` | No | Parallel workers (default `2`). |
| `--init` | No | Delete mirror state for this `--mirror` root in `mirrored_files`, then re-download everything (full re-mirror). Cannot be used with `--sync-ct`. |
| `--dry-run` | No | Log actions only; no HTTP download, no writes, no DB updates for mirroring (still connects to DB). |
| `--slack-token` | No | Bearer token for Slack `url_private` requests. If empty, **`SLACK_TOKEN`** is used. |
| `--aws-profile` | No | AWS shared config profile for S3 (e.g. SSO). If empty, **`AWS_PROFILE`** is used when set. |
| `--sync-ct` | No | **S3 only:** `HEAD` each `url_private` and update **Content-Type** on existing S3 objects to match Slack. Does not use `mirrored_files`. Cannot be combined with `--init`. |

**Local mirror example:**

```bash
export SLACK_TOKEN=xoxp-your-token
./slack-site mirror-files \
  --data ./data \
  --mirror file:///var/www/slack-files \
  --concurrency 4
```

**S3 mirror example:**

```bash
export SLACK_TOKEN=xoxp-your-token
export AWS_PROFILE=your-sso-profile
./slack-site mirror-files \
  --data ./data \
  --mirror s3://my-bucket/slack-files
```

For S3, the tool loads AWS config (profile/region); it discovers the bucket region to avoid redirect issues.

**Serve using the mirrored files:** If files are served at `https://cdn.example.com/slack-files/`, run:

```bash
./slack-site serve --data ./data --mirror https://cdn.example.com/slack-files
```

Paths are appended without a double slash (the server normalizes the mirror base).

### `reindex`

Rebuilds **`slack.bleve`** from **`slack.db`** message rows (overwrites the existing Bleve directory under `--data`). Use this if the database was updated without re-ingesting, or the search index was deleted or corrupted.

```text
slack-site reindex --data <data-dir>
```

| Flag | Required | Description |
|------|----------|-------------|
| `--data` | Yes | Directory containing `slack.db`. |

## Environment variables

| Variable | Used by | Purpose |
|----------|---------|---------|
| `SLACK_TOKEN` | `mirror-files` | Slack bearer token for authenticated download/HEAD of `url_private` URLs. |
| `AWS_PROFILE` | `mirror-files` (S3) | Default AWS profile when `--aws-profile` is not set. |

When mirroring to S3, static access keys in the environment may be cleared so the SDK prefers shared config / SSO; use a named profile when possible.

## Output layout

After a successful **ingest**, the `--data` directory typically contains:

```text
<data-dir>/
  slack.db      # SQLite database
  slack.bleve/  # Bleve index directory
```

`mirror-files` also creates or updates rows in `mirrored_files` inside `slack.db` for the chosen mirror root.

## Web UI

- **Home** — Entry point.
- **Channels / Private channels / DMs / MPIMs** — Lists with member counts where applicable.
- **Conversation** — Chronological messages, **older** first; **“Next (newer) messages”** loads the next page; **“First page”** returns to the oldest page.
- **Search** — Query string search over indexed message text; pagination when there are many hits.

File attachments show inline images when the MIME type is `image/*`; other types are download links.

## Development

Run tests:

```bash
make test
# or
go test ./...
```

## License

This project is licensed under the [MIT License](LICENSE).
