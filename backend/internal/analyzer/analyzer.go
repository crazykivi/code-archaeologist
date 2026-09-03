package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"code-archaeologist/backend/internal/llm"
	"code-archaeologist/backend/internal/scanner"
)

type Decision struct {
	Title       string   `json:"title"`
	Date        string   `json:"date,omitempty"`
	Commits     []string `json:"commits,omitempty"`
	CommitRange string   `json:"commit_range,omitempty"`
	Decision    string   `json:"decision"`
	Rationale   string   `json:"rationale,omitempty"`
	Impact      string   `json:"impact,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type ProgressCallback func(stage, message, details string, totalBatches, doneBatches, totalReduce, doneReduce int)

type CascadeConfig struct {
	Enabled     bool
	MaxParallel int
	ReduceSize  int
	BatchSize   int
}

type Params struct {
	SourceType   string
	Source       string
	ProviderName string
	Model        string
	Language     string
	ReportType   string

	Since      string
	Until      string
	FromCommit string
	ToCommit   string
}

const (
	ReportDecisions    = "decisions"
	ReportArchitecture = "architecture"
	ReportTechDebt     = "tech_debt"
	ReportTeam         = "team"
)

func NormalizeReportType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return ReportDecisions
	}
	return t
}

func IsSupportedReportType(t string) bool {
	switch NormalizeReportType(t) {
	case ReportDecisions, ReportArchitecture, ReportTechDebt, ReportTeam:
		return true
	default:
		return false
	}
}

func Run(
	ctx context.Context,
	provider llm.Provider,
	p Params,
	commits []scanner.CommitWithDiff,
	batchSize int,
) (string, error) {
	if batchSize <= 0 {
		batchSize = 20
	}

	var decisions []Decision

	for start := 0; start < len(commits); start += batchSize {
		end := start + batchSize
		if end > len(commits) {
			end = len(commits)
		}

		parsed, err := analyzeBatch(ctx, provider, p, commits[start:end])
		if err != nil {
			return "", err
		}
		decisions = append(decisions, parsed...)
	}

	return renderReport(p, "", decisions, len(commits)), nil
}

func RunCascade(
	ctx context.Context,
	provider llm.Provider,
	p Params,
	commits []scanner.CommitWithDiff,
	cascadeCfg CascadeConfig,
	onProgress ProgressCallback,
) (string, error) {
	batchSize := cascadeCfg.BatchSize
	if batchSize <= 0 {
		batchSize = 20
	}

	totalBatches := (len(commits) + batchSize - 1) / batchSize
	allDecisions := make([][]Decision, totalBatches)

	sem := make(chan struct{}, cascadeCfg.MaxParallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i := 0; i < len(commits); i += batchSize {
		batchIdx := i / batchSize
		start := i
		end := start + batchSize
		if end > len(commits) {
			end = len(commits)
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(idx, s, e int) {
			defer wg.Done()
			defer func() { <-sem }()

			decisions, err := analyzeBatch(ctx, provider, p, commits[s:e])

			mu.Lock()
			if err != nil {
				log.Printf("[Cascade] map batch %d failed: %v", idx, err)
				if firstErr == nil {
					firstErr = err
				}
				decisions = []Decision{}
			}
			allDecisions[idx] = decisions

			doneBatches := 0
			for _, d := range allDecisions {
				if d != nil {
					doneBatches++
				}
			}
			mu.Unlock()

			if onProgress != nil {
				onProgress("analyzing_map", "Анализ патчей (Map-Reduce)", fmt.Sprintf("Батч %d/%d", doneBatches, totalBatches), totalBatches, doneBatches, 0, 0)
			}
		}(batchIdx, start, end)
	}

	wg.Wait()

	if firstErr != nil {
		return "", fmt.Errorf("map phase failed: %w", firstErr)
	}

	var flatDecisions []Decision
	for _, d := range allDecisions {
		flatDecisions = append(flatDecisions, d...)
	}

	reduceSize := cascadeCfg.ReduceSize
	if reduceSize <= 0 {
		reduceSize = 50
	}

	totalReduceBatches := (len(flatDecisions) + reduceSize - 1) / reduceSize
	reducedDecisions := make([][]Decision, totalReduceBatches)

	for i := 0; i < len(flatDecisions); i += reduceSize {
		reduceIdx := i / reduceSize
		start := i
		end := start + reduceSize
		if end > len(flatDecisions) {
			end = len(flatDecisions)
		}

		batch := flatDecisions[start:end]
		reduced, err := reduceBatch(ctx, provider, p, batch)
		if err != nil {
			log.Printf("[Cascade] reduce batch %d failed: %v", reduceIdx, err)
			reduced = batch
		}

		reducedDecisions[reduceIdx] = reduced

		if onProgress != nil {
			onProgress(
				"reduce",
				"Консолидация решений",
				fmt.Sprintf("Объединён батч %d/%d", reduceIdx+1, totalReduceBatches),
				totalBatches,
				totalBatches,
				totalReduceBatches,
				reduceIdx+1,
			)
		}
	}

	var finalDecisions []Decision
	for _, d := range reducedDecisions {
		finalDecisions = append(finalDecisions, d...)
	}

	if onProgress != nil {
		onProgress(
			"finalize",
			"Генерация финального отчёта",
			"Обработка результатов и подготовка Markdown",
			totalBatches,
			totalBatches,
			totalReduceBatches,
			totalReduceBatches,
		)
	}

	overview := generateOverview(ctx, provider, p, finalDecisions)

	return renderReport(p, overview, finalDecisions, len(commits)), nil
}

func analyzeBatch(ctx context.Context, provider llm.Provider, p Params, batch []scanner.CommitWithDiff) ([]Decision, error) {
	messages := buildMessages(p, batch)

	content, err := provider.Chat(ctx, messages, llm.ChatOptions{
		Model:       p.Model,
		Temperature: 0.1,
	})
	if err != nil {
		return nil, err
	}

	decisions, err := ParseDecisions(content)
	if err != nil {
		log.Printf("[Analyzer] failed to parse LLM JSON: %v", err)
		decisions = []Decision{fallbackDecision(content)}
	}

	backfillBatchInfo(decisions, batch)
	return decisions, nil
}

func reduceBatch(ctx context.Context, provider llm.Provider, p Params, decisions []Decision) ([]Decision, error) {
	messages := buildReduceMessages(p, decisions)

	content, err := provider.Chat(ctx, messages, llm.ChatOptions{
		Model:       p.Model,
		Temperature: 0.05,
	})
	if err != nil {
		return nil, err
	}

	reduced, err := ParseDecisions(content)
	if err != nil {
		return decisions, nil
	}

	return reduced, nil
}

func generateOverview(ctx context.Context, provider llm.Provider, p Params, decisions []Decision) string {
	if len(decisions) == 0 {
		return ""
	}

	messages := buildFinalizeMessages(p, decisions)

	content, err := provider.Chat(ctx, messages, llm.ChatOptions{
		Model:       p.Model,
		Temperature: 0.1,
	})
	if err != nil {
		log.Printf("[Analyzer] overview generation failed: %v", err)
		return ""
	}

	var res struct {
		Overview string `json:"overview"`
	}
	if err := json.Unmarshal([]byte(extractJSON(content)), &res); err != nil {
		log.Printf("[Analyzer] failed to parse overview JSON: %v", err)
		return ""
	}
	return strings.TrimSpace(res.Overview)
}

func ParseDecisions(content string) ([]Decision, error) {
	s := extractJSONArray(content)
	if s == "" {
		return nil, fmt.Errorf("no JSON array found")
	}

	var raw []Decision
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, err
	}

	out := make([]Decision, 0, len(raw))
	for i := range raw {
		normalizeDecision(&raw[i])
		if raw[i].Title != "" || raw[i].Decision != "" {
			out = append(out, raw[i])
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("empty decisions")
	}

	return out, nil
}

func backfillBatchInfo(decisions []Decision, batch []scanner.CommitWithDiff) {
	if len(batch) == 0 {
		return
	}

	hashes := make([]string, 0, len(batch))
	dates := make([]string, 0, len(batch))
	for i := len(batch) - 1; i >= 0; i-- {
		hashes = append(hashes, shortHash(batch[i].Hash))
		if d := batch[i].Date; len(d) >= 10 {
			dates = append(dates, d[:10])
		}
	}

	rangeStr := hashes[0]
	if len(hashes) > 1 {
		rangeStr = hashes[0] + "…" + hashes[len(hashes)-1]
	}

	dateStr := ""
	if len(dates) > 0 {
		dateStr = dates[0]
		if last := dates[len(dates)-1]; last != dates[0] {
			dateStr = dates[0] + "…" + last
		}
	}

	for i := range decisions {
		if len(decisions[i].Commits) == 0 {
			decisions[i].Commits = hashes
		}
		if decisions[i].CommitRange == "" {
			decisions[i].CommitRange = rangeStr
		}
		if decisions[i].Date == "" {
			decisions[i].Date = dateStr
		}
	}
}

type reportMetaInfo struct {
	Title        string
	Section      string
	ItemsLabel   string
	NothingLabel string
	FieldLabel   string
}

var reportMeta = map[string]reportMetaInfo{
	ReportDecisions: {
		Title:        "История принятия решений",
		Section:      "Ключевые решения",
		ItemsLabel:   "Найдено решений",
		NothingLabel: "Решения не найдены.",
		FieldLabel:   "Решение",
	},
	ReportArchitecture: {
		Title:        "Эволюция архитектуры",
		Section:      "Архитектурные изменения",
		ItemsLabel:   "Найдено архитектурных изменений",
		NothingLabel: "Архитектурные изменения не найдены.",
		FieldLabel:   "Изменение",
	},
	ReportTechDebt: {
		Title:        "Технический долг в истории",
		Section:      "Записи о долге",
		ItemsLabel:   "Найдено записей о долге",
		NothingLabel: "Записи о долге не найдены.",
		FieldLabel:   "Факт",
	},
	ReportTeam: {
		Title:        "Анализ команды и вклада",
		Section:      "Наблюдения",
		ItemsLabel:   "Найдено наблюдений",
		NothingLabel: "Наблюдения не найдены.",
		FieldLabel:   "Вклад",
	},
}

func metaFor(reportType string) reportMetaInfo {
	if meta, ok := reportMeta[NormalizeReportType(reportType)]; ok {
		return meta
	}
	return reportMeta[ReportDecisions]
}

func renderReport(p Params, overview string, decisions []Decision, commitCount int) string {
	var b strings.Builder

	model := p.Model
	if model == "" {
		model = "default"
	}

	meta := metaFor(p.ReportType)

	b.WriteString("# " + meta.Title + "\n\n")
	fmt.Fprintf(&b, "- Источник: %s (%s)\n", p.Source, p.SourceType)
	fmt.Fprintf(&b, "- Сгенерировано: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Провайдер: %s\n", p.ProviderName)
	fmt.Fprintf(&b, "- Модель: %s\n", model)
	fmt.Fprintf(&b, "- Проанализировано коммитов: %d\n", commitCount)

	if p.Since != "" || p.Until != "" {
		since := p.Since
		if since == "" {
			since = "…"
		}
		until := p.Until
		if until == "" {
			until = "…"
		}
		fmt.Fprintf(&b, "- Период: %s — %s\n", since, until)
	}
	switch {
	case p.FromCommit != "" && p.ToCommit != "":
		fmt.Fprintf(&b, "- Диапазон коммитов: %s..%s\n", p.FromCommit, p.ToCommit)
	case p.FromCommit != "":
		fmt.Fprintf(&b, "- Диапазон коммитов: %s..HEAD\n", p.FromCommit)
	case p.ToCommit != "":
		fmt.Fprintf(&b, "- Диапазон коммитов: до %s\n", p.ToCommit)
	}

	fmt.Fprintf(&b, "- %s: %d\n\n", meta.ItemsLabel, len(decisions))

	if overview != "" {
		b.WriteString("## Обзор изменений проекта\n\n")
		fmt.Fprintf(&b, "%s\n\n", overview)
		b.WriteString("## " + meta.Section + "\n\n")
	}

	if len(decisions) == 0 {
		b.WriteString(meta.NothingLabel + "\n")
		return b.String()
	}

	heading := "##"
	if overview != "" {
		heading = "###"
	}

	for i, d := range decisions {
		title := d.Title
		if title == "" {
			title = "Без названия"
		}

		date := d.Date
		if date == "" {
			date = "неизвестно"
		}

		fmt.Fprintf(&b, "%s %d. %s\n\n", heading, i+1, title)
		fmt.Fprintf(&b, "- Дата: %s\n", date)
		if d.CommitRange != "" {
			fmt.Fprintf(&b, "- Коммиты: %s\n", d.CommitRange)
		} else if len(d.Commits) > 0 {
			fmt.Fprintf(&b, "- Коммиты: `%s`\n", strings.Join(d.Commits, "`, `"))
		}
		if len(d.Tags) > 0 {
			fmt.Fprintf(&b, "- Теги: %s\n", strings.Join(d.Tags, ", "))
		}
		b.WriteString("\n")

		if d.Decision != "" {
			fmt.Fprintf(&b, "**%s:** %s\n\n", meta.FieldLabel, d.Decision)
		}
		if d.Rationale != "" {
			fmt.Fprintf(&b, "**Обоснование:** %s\n\n", d.Rationale)
		}
		if d.Impact != "" {
			fmt.Fprintf(&b, "**Влияние:** %s\n\n", d.Impact)
		}
	}

	return b.String()
}

func buildMessages(p Params, batch []scanner.CommitWithDiff) []llm.Message {
	type commitView struct {
		Hash    string `json:"hash"`
		Author  string `json:"author,omitempty"`
		Date    string `json:"date"`
		Subject string `json:"subject"`
		Body    string `json:"body,omitempty"`
		Diff    string `json:"diff,omitempty"`
	}

	showAuthor := NormalizeReportType(p.ReportType) == ReportTeam

	views := make([]commitView, 0, len(batch))
	for _, c := range batch {
		v := commitView{
			Hash:    shortHash(c.Hash),
			Date:    c.Date,
			Subject: c.Subject,
			Body:    truncateText(c.Body, 800),
			Diff:    truncateText(c.Diff, 2000),
		}
		if showAuthor {
			v.Author = c.AuthorName
		}
		views = append(views, v)
	}

	data, err := json.Marshal(views)
	if err != nil {
		data = []byte("[]")
	}

	set := promptsFor(p.ReportType)
	system := fmt.Sprintf(set.system, languageLabel(p.Language))
	user := fmt.Sprintf(baseUserPrompt, p.SourceType, p.Source, string(data), set.instruction)

	return []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
}

func buildReduceMessages(p Params, decisions []Decision) []llm.Message {
	data, err := json.Marshal(decisions)
	if err != nil {
		data = []byte("[]")
	}

	system := reduceSystemPrompt
	user := fmt.Sprintf(reduceUserPrompt, string(data))

	return []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
}

func buildFinalizeMessages(p Params, decisions []Decision) []llm.Message {
	data, err := json.Marshal(decisions)
	if err != nil {
		data = []byte("[]")
	}

	system := finalizeSystemPrompt
	user := fmt.Sprintf(finalizeUserPrompt, string(data))

	return []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
}

func extractJSONArray(s string) string {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return "{}"
	}
	return s[start : end+1]
}

func normalizeDecision(d *Decision) {
	d.Title = truncateText(strings.TrimSpace(d.Title), 300)
	d.Date = strings.TrimSpace(d.Date)
	d.CommitRange = truncateText(strings.TrimSpace(d.CommitRange), 200)
	d.Decision = truncateText(strings.TrimSpace(d.Decision), 4000)
	d.Rationale = truncateText(strings.TrimSpace(d.Rationale), 4000)
	d.Impact = truncateText(strings.TrimSpace(d.Impact), 4000)

	if len(d.Commits) > 100 {
		d.Commits = d.Commits[:100]
	}
	for i := range d.Commits {
		d.Commits[i] = strings.TrimSpace(d.Commits[i])
	}

	if len(d.Tags) > 20 {
		d.Tags = d.Tags[:20]
	}
	for i := range d.Tags {
		d.Tags[i] = strings.TrimSpace(d.Tags[i])
	}
}

func fallbackDecision(content string) Decision {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "Пустой ответ модели"
	}

	d := Decision{
		Title:    "Неструктурированный ответ модели",
		Decision: truncateText(content, 4000),
	}
	normalizeDecision(&d)
	return d
}

func languageLabel(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if strings.HasPrefix(l, "en") {
		return "English"
	}
	if strings.HasPrefix(l, "ru") {
		return "Russian"
	}
	if l == "" {
		return "Russian"
	}
	return lang
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func truncateText(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// Общая жёсткая основа для всех типов отчётов: только факты из diff/сообщений.
const baseFactsPrompt = `Ты — строгий и беспристрастный парсер Git-истории. Твоя единственная задача — зафиксировать ФАКТЫ изменений в коде.

ЖЕСТКИЕ ЗАПРЕТЫ (Нарушение = провал):
1. ЗАПРЕЩЕНО писать "воду", лирику, оценочные суждения ("хороший код", "монолит", "улучшили", "стало чище", "архитектурный компромисс").
2. ЗАПРЕЩЕНО выдумывать контекст, бизнес-логику или "эволюцию проекта", если этого нет в явном виде в diff.
3. ЗАПРЕЩЕНО описывать весь код или выводить его в ответ. Описывай ТОЛЬКО дельту (что ДОБАВЛЕНО, УДАЛЕНО или ИЗМЕНЕНО).

ПРАВИЛА ФОРМИРОВАНИЯ ОТВЕТА:
1. Если передан diff: пиши только то, что физически изменилось в файлах (например: "В файле X добавлена функция Y", "Изменен SQL-запрос: добавлен JOIN", "Удалена зависимость Z").
2. Если diff НЕ передан: пиши только суть из сообщения коммита.
3. В поле "commit_range" ОБЯЗАТЕЛЬНО укажи диапазон хэшей (с какого по какой), в "commits" — точные хэши.
4. Пиши максимально кратко и сухо. Факты, имена файлов, имена функций, пакеты.

Ответ должен быть строго валидным JSON-массивом объектов без markdown, без пояснений и без вывода самого кода.
Формат объекта:
{"title": string, "date": string, "commits": [string], "commit_range": string, "decision": string, "rationale": string, "impact": string, "tags": [string]}

Поля:
- title: Краткая суть изменения (например: "Добавлен индекс в таблицу users").
- date: Дата в формате ГГГГ-ММ-ДД (если видна из коммита).
- commit_range: Диапазон хэшей (с какого по какой).
- commits: Массив точных хэшей коммитов.
- decision: Сухой факт изменения (что именно сделали).
- rationale: Причина (если указана в коммите) или "Не указана".
- impact: На какие файлы/модули повлияло.
- tags: Короткие метки (например: "ci", "deps", "db").

Язык ответа: %s.`

const baseUserPrompt = `Репозиторий: %s (%s)

Коммиты с изменениями кода в JSON:
%s

Задача: %s

Верни только JSON-массив.`

type promptSet struct {
	system      string
	instruction string
}

var reportPrompts = map[string]promptSet{
	ReportDecisions: {
		system:      baseFactsPrompt,
		instruction: "Собери технические решения, которые можно обоснованно извлечь из этих коммитов",
	},
	ReportArchitecture: {
		system: baseFactsPrompt + `

СПЕЦИАЛИЗАЦИЯ — АРХИТЕКТУРА:
Фиксируй только структурные изменения: появление и удаление модулей и слоёв, смена каркасных фреймворков и библиотек, изменение схемы данных, разделение и слияние сервисов, смена протоколов взаимодействия. Мелкие правки внутри одного файла без структурного значения пропускай.
В "tags" используй метки: "layer", "framework", "data-model", "integration", "build".`,
		instruction: "Собери архитектурные изменения (структурные сдвиги уровня модулей, слоёв, фреймворков, схемы данных)",
	},
	ReportTechDebt: {
		system: baseFactsPrompt + `

СПЕЦИАЛИЗАЦИЯ — ТЕХНИЧЕСКИЙ ДОЛГ:
Фиксируй только следы долга и его погашения: пометки TODO, FIXME, HACK, XXX, WORKAROUND; откат изменений (revert); отключенные и пропущенные тесты; хотфиксы; дублирование кода; закомментированный код.
В "decision" пиши, что именно появилось или что было устранено. В "tags" используй метки: "todo", "fixme", "hack", "workaround", "revert", "skipped-test", "hotfix". Коммиты без признаков долга пропускай.`,
		instruction: "Собери записи о техническом долге и его погашении (TODO/FIXME/HACK, revert-ы, отключенные тесты, workaround-ы, хотфиксы)",
	},
	ReportTeam: {
		system: baseFactsPrompt + `

СПЕЦИАЛИЗАЦИЯ — КОМАНДА И ВКЛАД:
Анализируй вклад участников по полю "author" из входных данных. Группируй наблюдения по подсистемам, а не по отдельным коммитам.
Фиксируй: кто какие подсистемы и модули менял; зоны концентрации изменений у одного автора; соотношение фич, фиксов и рефакторинга по сообщениям коммитов; bus-фактор — сколько авторов меняли каждый затронутый модуль.
Не выдумывай имена: используй только те, что есть во входных данных. Персональных оценок не давай — только фактическое распределение.
В "title" указывай автора или область (например: "Автор X: зона ответственности"). В "impact" перечисляй модули, где активность максимальна.`,
		instruction: "Проанализируй вклад авторов: зоны ответственности, специализацию, соотношение типов коммитов, bus-фактор по затронутым модулям",
	},
}

func promptsFor(reportType string) promptSet {
	if set, ok := reportPrompts[NormalizeReportType(reportType)]; ok {
		return set
	}
	return reportPrompts[ReportDecisions]
}

const reduceSystemPrompt = `Ты — строгий редактор технической документации.
Твоя задача:
1. Объединить связанные изменения в одну запись.
2. Жестко вычистить любую "воду", эмоции, рассуждения об архитектуре и оценочные суждения.
3. Оставить только сухие факты: какие файлы изменены, какие функции добавлены/удалены.
4. Сохранить точные хэши коммитов и актуализировать "commit_range".
5. Выстроить в хронологическом порядке.

Верни ТОЛЬКО валидный JSON-массив. Никакого текста вокруг.`

const reduceUserPrompt = `Объедини эти решения в один список:

%s

Верни только JSON-массив.`

const finalizeSystemPrompt = `Ты — генератор сухого Changelog (списка изменений).

На вход ты получаешь список уже зафиксированных изменений. Твоя ЕДИНСТВЕННАЯ задача — написать краткий обзор (overview). Решения не переписывай и не возвращай.

ТРЕБОВАНИЯ К ОБЗОРУ:
1. Стиль: предельно сухой, технический, как в git log.
2. ЗАПРЕЩЕНО писать про "эволюцию", "архитектурные компромиссы", "монолиты", "микросервисы", "точки бифуркации".
3. Только факты: какие подсистемы затронуты, какие крупные фичи добавлены, какие баги исправлены.
4. Никакой лирики и "воды". Максимум 3-4 предложения.

Верни JSON вида: {"overview": string}.
Обзор должен быть на русском языке.`

const finalizeUserPrompt = `Сгенерируй обзор по этим изменениям:

%s

Верни JSON только с полем overview.`
