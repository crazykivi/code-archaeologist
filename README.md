# Code Archaeologist

![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vuedotjs&logoColor=white)
![TypeScript](https://img.shields.io/badge/TypeScript-5-3178C6?logo=typescript&logoColor=white)
![SQLite](https://img.shields.io/badge/SQLite-3-003B57?logo=sqlite&logoColor=white)

## О проекте

**Code Archaeologist** — инструмент для помощи в документировании изменений из Git-истории. Он извлекает коммиты из репозитория (локального или GitHub/GitLab), группирует их в батчи и с помощью LLM строит структурированные отчёты: о принятых технических решениях, эволюции архитектуры, техническом долге и вкладе участников.

> 🤖 Проект разрабатывается **с помощью ИИ** (как и данная документация): код, тесты и документация пишутся в паре с LLM под контролем человека.

## Особенности

- **4 типа отчётов**: ключевые решения / эволюция архитектуры / технический долг / команда и вклад
- **Настройки провайдеров через UI**: Base URL, модель, ключ и кастомные заголовки хранятся в БД и перекрывают `.env`; плейсхолдер `{{api_key}}` в заголовках подставляется при запросе
- **Фильтры коммитов**: период (с/по дату) и диапазон коммитов (`от..до`, теги, ветки)
- **Каркас из шести провайдеров LLM**: Ollama, llama.cpp, OpenAI, DeepSeek, Qwen, любой OpenAI-совместимый (`CUSTOM_*`)
- **Map-Reduce (каскад)**: батчевый анализ + консолидация результатов для длинной истории
- **Анализ diff** с ограничением размера на коммит
- **Прогресс задач в реальном времени**: стадии, батчи, ETA
- **История анализов** с хранением отчётов в SQLite и экспортом в Markdown
- **Безопасность**: rate limiting per-IP, CORS-whitelist, SSRF-защита (whitelist git-хостов), валидация входных данных, redaction кредов в логах

## Технологический стек

| Категория | Технологии |
|:--|:--|
| Backend | Go 1.25, Gin, SQLite (modernc.org/sqlite, без CGO) |
| Frontend | Vue 3, TypeScript, Pinia, Vue Router, Tailwind CSS, Vite |
| Markdown | marked + DOMPurify (санитизация) |
| LLM | OpenAI-compatible chat completions API |

## Быстрый старт

Требования: **Go 1.25+**, **Node 20+**, установленный `git` в PATH.

```bash
# 1. Backend
cd backend
cp .env.example .env   # при необходимости отредактируйте
go run .

# 2. Frontend (в отдельном терминале)
cd frontend
npm install
npm run dev
```

Откройте http://localhost:5173 — API проксируется на backend (:8080).

## Сборка и тестирование

```bash
# Backend
cd backend
go test ./...
go build -o code-archaeologist .

# Frontend
cd frontend
npm run type-check
npm run build
```

## Конфигурация

Backend читает переменные окружения; локально — из `backend/.env` (шаблон: `backend/.env.example`). Основные:

| Переменная | По умолчанию | Описание |
|:--|:--|:--|
| `PORT` | `8080` | Порт API |
| `DB_PATH` | `data.db` | Путь к SQLite (`:memory:` — в память) |
| `ALLOW_CORS` | — | Origin через запятую (пусто = CORS выключен) |
| `DEFAULT_LLM_PROVIDER` | `ollama` | `ollama` / `openai` / `deepseek` / `qwen` / `llamacpp` / `custom` |
| `GIT_ALLOWED_HOSTS` | `github.com,gitlab.com` | Whitelist хостов для клонирования |
| `LOCAL_ROOTS` | — | Whitelist путей для анализа локальных репозиториев |
| `RATE_LIMIT_ANALYZE_PER_MINUTE` | `5` | Rate limit на запуск анализа |

> ⚠️ `.env` никогда не коммитится. Ключи провайдеров (`OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, …) задаются только в окружении.

## Структура

```
├── backend/                  # Go API
│   └── internal/
│       ├── analyzer/         # LLM-анализ: промпты, каскад Map-Reduce, рендер отчётов
│       ├── scanner/          # Git: клонирование, log, diff, фильтры коммитов
│       ├── llm/              # Провайдеры LLM (ollama, openai-compatible)
│       ├── handlers/         # HTTP-обработчики, фоновые задачи
│       ├── middleware/       # Rate limiting, CORS
│       ├── store/            # SQLite / in-memory хранилище задач и отчётов
│       ├── config/           # Конфигурация из env
│       └── server/           # Роуты, body limit
└── frontend/                 # Vue 3 SPA
    └── src/
        ├── views/            # Форма запуска, история, просмотр отчёта
        ├── components/
        ├── stores/           # Pinia: задачи, поллинг
        ├── services/         # API-слой
        └── composables/      # Markdown-рендер
```

## Как это работает

1. Пользователь выбирает источник (локальный путь / GitHub / GitLab), тип отчёта, фильтры коммитов и провайдера LLM.
2. Backend ставит задачу в очередь (`POST /api/v1/analyze`), фоновая горутина готовит репозиторий (клон или локальный путь с проверкой whitelist).
3. `git log` с фильтрами → батчи коммитов (+ diff при включённой опции) → LLM извлекает структурированные факты → (каскад) консолидация → финальный Markdown-отчёт.
4. Отчёт сохраняется в SQLite и доступен через UI или `GET /api/v1/reports/:id`.
