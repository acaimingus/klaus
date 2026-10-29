# Klaus

**Klaus** is a basic, minimalist Discord bot written in Go — built as a small fun project for private use on a personal Discord server.

Instead of acting like a formal, overly polite AI assistant, Klaus behaves like a regular (slightly dry) Discord user: he keeps his replies short and casual whenever someone pings him or replies to his messages. The bot is intended for conversational purposes.

## Features

- **Responds to Pings & Replies:** Klaus only chimes in when directly mentioned (`@Klaus`) or replied to with a mention (an empty ping just gets a dry `"was"` in response).
- **Context-Aware:** Reads the last 10 messages in the channel as background context to naturally follow the ongoing conversation.
- **Pollinations.ai Integration:** Uses the OpenAI-compatible API provided by [Pollinations.ai](https://pollinations.ai/) by default.
- **Built-in Queue & Rate Limiting:** Processes messages sequentially with a 30-second cooldown and an automatic retry on `HTTP 429`.

## Requirements & Setup

1. **Go** (1.24+)
2. A **Discord Bot Token** (with the *Message Content Intent* enabled in the [Discord Developer Portal](https://discord.com/developers/applications)).

### Environment Variables

| Variable | Description | Default |
| :--- | :--- | :--- |
| `KLAUS_DISCORD_BOT_TOKEN` | **Required:** Discord Bot Token | – |
| `KLAUS_POLLINATIONS_API_KEY` | Optional API key for Pollinations | *(empty)* |
| `KLAUS_POLLINATIONS_MODEL` | Model name to use | `openai` |
| `KLAUS_POLLINATIONS_URL` | API endpoint URL | `https://text.pollinations.ai/openai` |
| `KLAUS_SYSTEM_PROMPT` | Custom system prompt for Klaus's personality | *(default Klaus prompt)* |

### Running the Bot

```bash
export KLAUS_DISCORD_BOT_TOKEN="your_token_here"
go build -o klaus .
./klaus
```

## Forks by Friends

This repository intentionally keeps Klaus very basic and lightweight. Friends of mine have forked the project and built their own expanded versions — check them out:

- [**TimuYsl/klaus-1.1**](https://github.com/TimuYsl/klaus-1.1)
- [**Mosawer05/klaus-smart**](https://github.com/Mosawer05/klaus-smart)
