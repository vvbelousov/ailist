# AIList

Self-hosted учёт домашних запасов. Фраза «муки осталось 2 кг, колбаса кончилась» превращается в черновик изменений. Проверьте названия, количества, единицы и нажмите **Подтвердить**. Остатки, сроки, покупки и история доступны в браузере и через личный чат с ботом. [Architecture and roadmap](docs/architecture.md) · [Apache-2.0](LICENSE).

## Установка / Quick start

Требуются Git, Docker Engine с Compose v2 и OpenSSL. Команды для Linux:

```sh
git clone https://github.com/vvbelousov/ailist.git
cd ailist
sh scripts/init-env.sh
docker compose up -d --build --wait
```

Откройте http://127.0.0.1:8080 и введите значение APP_TOKEN из .env. Язык переключается в шапке. Для доступа из дома используйте HTTPS reverse proxy на этом же хосте или SSH-туннель. По умолчанию только приложение публикует порт, на loopback. Один экземпляр = одно доверенное домохозяйство, один ключ и общий остаток. PostgreSQL и распознавание речи доступны внутри Compose-сети.

**English.** Run the commands above, open the local URL and sign in with APP_TOKEN from .env. Select EN in the header. Manual entry works immediately. Select a row to update a lot. Set an absolute amount, add, consume or transfer a quantity. Review and confirm. Each combination of name, unit, location and expiry identifies a lot. Shopping shows depleted items with no positive lot of the same name and unit. Expiring includes overdue lots and the next seven days. History shows the latest 100 confirmed batches. For LAN access use HTTPS or an SSH tunnel.

## Локальная модель / Local LLM

Запустите llama-server на вашей машине или укажите существующий v1 endpoint в локальной сети. Пример для уже скачанного GGUF:

```sh
llama-server -m /models/instruct.gguf --alias household --host 0.0.0.0 --port 8081 -c 8192
```

В .env задайте LLM_BASE_URL=http://host.docker.internal:8081/v1 и LLM_MODEL=household; у внешней машины подставьте её LAN-адрес. Закройте доступ к порту модели извне. При включённой авторизации модели задайте LLM_API_KEY. Endpoint должен поддерживать POST /v1/chat/completions и возвращать JSON в message.content. Backend передаёт текст команды и до 150 партий для сопоставления имён. После изменения .env выполните `docker compose up -d`.

На CPU проверен llama.cpp с Qwen2.5-1.5B-Instruct Q4_K_M. Фраза «Риса осталось 2 кг, а стиральный порошок закончился» дала две операции, после подтверждения порошок появился в покупках. Это проверка конкретного сценария; качество распознавания сложных команд зависит от модели.

**English.** Point LLM_BASE_URL at your local OpenAI-compatible endpoint, including /v1, and set LLM_MODEL to its model ID. Use an instruction-tuned multilingual model. No model provider account is required. Queries read the database. Writes require review. Clarifications are single-turn: submit the full revised request.

## Голос / Voice

```sh
docker compose --profile voice up -d --build
docker compose logs -f whisper
```

При первом запуске faster-whisper скачивает multilingual tiny в том models. Для работы без интернета заранее загрузите CTranslate2-модель и примонтируйте каталог в whisper (например /weights:ro), задайте WHISPER_MODEL=/weights. На CPU используется int8, по умолчанию два потока и одна транскрипция одновременно. В веб-клиенте выберите аудиофайл до 12 MiB и 2 минут, проверьте распознанный текст и нажмите «Разобрать».

**English.** Enable the voice profile for local CPU transcription. The first start downloads model weights. Mount a predownloaded model and set WHISPER_MODEL to its container path for offline use. For an air-gapped installation also prebuild and transfer the container images. Runtime internet access is only needed for optional Telegram.

## Telegram

Создайте бота через BotFather. Укажите TELEGRAM_TOKEN, TELEGRAM_USERS (числовые Telegram user ID через запятую) и BOT_LANG=ru или en. Выполните `docker compose up -d`. Нужен исходящий HTTPS к Telegram. Бот обрабатывает только личные сообщения пользователей из allowlist. Текст и голос в этом канале проходят через Telegram. Входящие порты и webhook не требуются.

**English.** Configure the bot token, comma-separated allowed numeric user IDs, and bot language. Send text or a voice note. Use /list, /shopping, /expire, /cancel or the exact /confirm command under the preview. Each chat holds its latest draft. Repeating a confirmation does not repeat the change.

## Эксплуатация / Operations

```sh
docker compose ps
docker compose logs --tail=100 app
curl -fsS http://127.0.0.1:8080/health
docker compose exec -T db pg_dump -U ailist -Fc ailist > household.dump
```

To restore into a **fresh** instance, start only the database, wait for readiness, import the dump and start the app:

```sh
docker compose up -d --wait db
docker compose exec -T db pg_restore -U ailist -d ailist --single-transaction < household.dump
docker compose up -d --build --wait
```

Keep .env private and back up the database. The generated database password uses URL-safe hex. Changing POSTGRES_PASSWORD after initialization requires rotating the password in PostgreSQL as well. Stock and bot state survive container restarts. To update, back up, check out the chosen version, then rebuild with the same Compose project name and volumes. Include --profile voice if used.

## Проверки / Development

```sh
go vet ./...
go test -race ./...
(cd web && npm ci && npm run check && npm run build)
```

Set TEST_DATABASE_URL to a **disposable database** to run PostgreSQL integration tests (they truncate application tables). Tests cover replay, rollback, concurrent deductions, transfer, expiry, shopping and model/Telegram contracts. CI also builds containers, signs in through Chromium, enters stock, checks history, switches language and verifies persistence after app restart. The release job runs only after verification.
