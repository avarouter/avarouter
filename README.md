# avarouter

![logo](logo.png)

> **This repo powers [avarouter](https://avarouter.up.railway.app) — the Avalanche Native AI Gateway.** AGW is the underlying Go reverse-proxy / prepaid-LLM-router; the `demo/` and `skills/` subtrees provide a CLI and a Mavis skill for end-user onboarding.

```
agw/                      ← this repo (single repo, branch x402)
├── *.go                   ←  server (Go)
├── cmd/agw/               ←  main
├── index.html             ←  landing page
├── og-agw-x402.jpg        ←  OG image
├── demo/                  ←  local run + CLI source of truth
│   ├── start.sh
│   ├── start-mock.sh
│   └── node/agw-cli/      ←  the `agw` CLI (14 subcommands)
└── skills/
    └── agw-onboard/       ←  Mavis SKILL (vendored CLI + examples)
```

**Repo layout** is a single branch (`x402`); `demo/` and `skills/` are subtrees that the Mavis skill syncer auto-uploads.

