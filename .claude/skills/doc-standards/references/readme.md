# README template

```markdown
# 📦 <Project name>

<One sentence: what it does and for whom.>

<!-- toc -->
## 📑 Contents

- [Quick start](#-quick-start)
- [Configuration](#-configuration)
- [Common tasks](#-common-tasks)
- [Project layout](#-project-layout)
- [Architecture](#-architecture)
- [Contributing](#-contributing)
- [License](#-license)

<!-- /toc -->

## 🚀 Quick start
<Prerequisites (versions).>

    <clone / install command>
    <configure: copy .env.example to .env and fill in the variable NAMES listed below>
    <run command>

<What success looks like (URL, output).>

## 🔧 Configuration
| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | yes | Postgres connection string |

## ⚡ Common tasks
| Task | Command |
|---|---|
| Run tests | `<cmd>` |
| Lint / format | `<cmd>` |
| Build | `<cmd>` |

## 📂 Project layout
<5–10 lines: top-level dirs and what lives there. Link to the architecture doc/diagram.>

## 🧱 Architecture
<One paragraph + diagram link. Key decisions → docs/adr/.>

## 🙋 Contributing
<Branching, PR checks, code style, how to run CI locally.>

## 📜 License
```

Rules:
- Quick start must work from a clean clone. Verify every command.
- Don't paste config values. Name the variables and point to `.env.example`.
