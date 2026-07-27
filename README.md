# Gibson

## Development

Run Vite and Gibson in separate terminals from the repository root:

```sh
npm run dev --prefix web
```

```sh
go run . serve --dev
```

Vite listens on `http://localhost:5173`, but that is only Gibson's internal proxy target. **Do not open the Vite URL directly**: API requests made there never reach Gibson.

Open the Gibson URL printed by the second command instead. With this repository's default `gibson.toml`, that URL is `http://127.0.0.1:7311/`.
