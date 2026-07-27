# Gibson

## Development

Run Vite and Gibson in separate terminals from the repository root:

```sh
make dev-web
```

```sh
make dev-server
```

Vite listens on `http://localhost:5173`, but that is only Gibson's internal proxy target. **Do not open the Vite URL directly**: API requests made there never reach Gibson.

Open the Gibson URL printed by `make dev-server` instead. With this repository's default `gibson.toml`, that URL is `http://127.0.0.1:7311/`.

The Make targets wrap these underlying tools; `make dev-server` also injects the current Git version:

```sh
npm run dev --prefix web
go run . serve --dev
```
