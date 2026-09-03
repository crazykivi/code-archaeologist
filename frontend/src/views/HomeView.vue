<template>
  <div class="animate-slide-up space-y-6">
    <div class="mb-8">
      <h1 class="text-2xl font-bold tracking-tight text-white">Новый анализ</h1>
      <p class="mt-1.5 text-sm text-gray-500">
        Извлеките технические решения из истории коммитов с помощью LLM
      </p>
    </div>

    <div class="glass-card gradient-border p-8">
      <form @submit.prevent="handleSubmit" class="space-y-6">
        <div class="grid gap-6 sm:grid-cols-2">
          <div class="space-y-2">
            <label class="text-sm font-medium text-gray-300">Тип источника</label>
            <select v-model="form.source_type" class="select-field">
              <option value="local">Локальный репозиторий</option>
              <option value="github">GitHub</option>
              <option value="gitlab">GitLab</option>
            </select>
          </div>

          <div class="space-y-2">
            <label class="text-sm font-medium text-gray-300">Лимит коммитов</label>
            <input
              v-model.number="form.limit"
              type="number"
              min="1"
              max="10000"
              placeholder="0 — без лимита"
              class="input-field"
            />
            <p class="text-xs text-gray-600">
              Максимум коммитов для анализа, начиная с самых новых
            </p>
          </div>
        </div>

        <div class="space-y-2">
          <label class="text-sm font-medium text-gray-300">
            {{ form.source_type === "local" ? "Путь к репозиторию" : "URL репозитория" }}
          </label>
          <input
            v-model="form.source"
            :placeholder="
              form.source_type === 'local'
                ? '~/projects/my-repo'
                : 'https://github.com/user/repo'
            "
            class="input-field font-mono text-[13px]"
            required
          />
        </div>

        <div class="space-y-2">
          <label class="text-sm font-medium text-gray-300">Тип отчёта</label>
          <select v-model="form.report_type" class="select-field">
            <option
              v-for="rt in reportTypes"
              :key="rt.value"
              :value="rt.value"
            >
              {{ rt.label }}
            </option>
          </select>
          <p class="text-xs text-gray-600">{{ reportTypeHint }}</p>
        </div>

        <div class="space-y-2">
          <button
            type="button"
            class="flex w-full items-center justify-between text-sm font-medium text-gray-300"
            @click="showFilters = !showFilters"
          >
            <span class="flex items-center gap-2">
              Фильтры коммитов
              <span
                v-if="activeFiltersCount"
                class="rounded-full bg-accent-500/15 px-2 py-0.5 text-xs font-medium text-accent-400"
              >
                {{ activeFiltersCount }}
              </span>
            </span>
            <svg
              class="h-4 w-4 transition-transform"
              :class="{ 'rotate-180': showFilters }"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
            >
              <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
            </svg>
          </button>

          <div v-if="showFilters" class="space-y-4 rounded-xl border border-white/[0.04] bg-white/[0.02] p-4">
            <div class="grid gap-4 sm:grid-cols-2">
              <div class="space-y-2">
                <label class="text-xs text-gray-400">Период с</label>
                <input
                  v-model="form.since"
                  type="date"
                  class="input-field"
                  :max="form.until || undefined"
                />
              </div>
              <div class="space-y-2">
                <label class="text-xs text-gray-400">Период по</label>
                <input
                  v-model="form.until"
                  type="date"
                  class="input-field"
                  :min="form.since || undefined"
                />
              </div>
            </div>

            <div class="grid gap-4 sm:grid-cols-2">
              <div class="space-y-2">
                <label class="text-xs text-gray-400">От коммита (исключая)</label>
                <input
                  v-model="form.from_commit"
                  placeholder="хэш, тег или ветка"
                  class="input-field font-mono text-[13px]"
                />
              </div>
              <div class="space-y-2">
                <label class="text-xs text-gray-400">До коммита</label>
                <input
                  v-model="form.to_commit"
                  placeholder="хэш, тег или ветка"
                  class="input-field font-mono text-[13px]"
                />
              </div>
            </div>

            <p class="text-xs text-gray-600">
              Сначала отбираются коммиты по периоду и/или диапазону «от…до» (исключая «от»), затем лимит ограничивает количество — анализируются самые свежие из отобранных. Оставьте поля пустыми, чтобы анализировать всю историю.
            </p>
          </div>
        </div>

        <div class="space-y-2">
          <label class="text-sm font-medium text-gray-300">Провайдер LLM</label>
          <select v-model="form.provider" class="select-field">
            <option value="">По умолчанию</option>
            <option
              v-for="p in availableProviders"
              :key="p.name"
              :value="p.name"
              :disabled="!p.configured"
            >
              {{ p.name }}{{ !p.configured ? " (не настроен)" : "" }}
            </option>
          </select>
        </div>

        <div class="flex flex-wrap items-center gap-6 pt-2">
          <label class="group flex cursor-pointer items-center gap-3">
            <div class="relative">
              <input v-model="form.cascade" type="checkbox" class="peer sr-only" />
              <div
                class="h-5 w-9 rounded-full bg-white/[0.08] transition-colors peer-checked:bg-accent-500"
              ></div>
              <div
                class="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-gray-400 transition-all peer-checked:translate-x-4 peer-checked:bg-white"
              ></div>
            </div>
            <span
              class="text-sm text-gray-300 transition-colors group-hover:text-gray-100"
            >
              Map-Reduce
            </span>
          </label>

          <label class="group flex cursor-pointer items-center gap-3">
            <div class="relative">
              <input v-model="form.diff" type="checkbox" class="peer sr-only" />
              <div
                class="h-5 w-9 rounded-full bg-white/[0.08] transition-colors peer-checked:bg-accent-500"
              ></div>
              <div
                class="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-gray-400 transition-all peer-checked:translate-x-4 peer-checked:bg-white"
              ></div>
            </div>
            <span
              class="text-sm text-gray-300 transition-colors group-hover:text-gray-100"
            >
              Анализ diff
            </span>
          </label>
        </div>

        <div class="flex items-center justify-end border-t border-white/[0.04] pt-6">
          <button type="submit" :disabled="isSubmitting" class="btn-primary">
            <svg
              v-if="isSubmitting"
              class="h-4 w-4 animate-spin"
              viewBox="0 0 24 24"
              fill="none"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              />
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
              />
            </svg>
            <svg
              v-else
              class="h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"
              />
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
              />
            </svg>
            {{ isSubmitting ? "Запуск…" : "Начать анализ" }}
          </button>
        </div>
      </form>
    </div>

    <transition name="fade">
      <div v-if="error" class="glass-card border-red-500/20 bg-red-500/[0.04] p-5">
        <div class="flex items-start gap-3">
          <div
            class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-red-500/10"
          >
            <svg
              class="h-4 w-4 text-red-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z"
              />
            </svg>
          </div>
          <div class="flex-1">
            <p class="text-sm font-medium text-red-300">Не удалось запустить анализ</p>
            <p class="mt-1 text-sm text-red-400/80">{{ error }}</p>
          </div>
          <button @click="error = null" class="text-red-400/60 hover:text-red-300">
            <svg
              class="h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M6 18L18 6M6 6l12 12"
              />
            </svg>
          </button>
        </div>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from "vue";
import { useRouter } from "vue-router";
import { useJobsStore } from "@/stores/jobs";
import { analyzeRepository, getProviders } from "@/services/api";
import type { Provider, AnalyzeRequest, ReportType } from "@/types";

const router = useRouter();
const jobsStore = useJobsStore();

const reportTypes: { value: ReportType; label: string; hint: string }[] = [
  {
    value: "decisions",
    label: "Ключевые решения",
    hint: "Технические решения, извлечённые из истории коммитов",
  },
  {
    value: "architecture",
    label: "Эволюция архитектуры",
    hint: "Структурные изменения: модули, слои, фреймворки, схема данных",
  },
  {
    value: "tech_debt",
    label: "Технический долг",
    hint: "TODO/FIXME/HACK, revert-ы, отключенные тесты, workaround-ы",
  },
  {
    value: "team",
    label: "Команда и вклад",
    hint: "Зоны ответственности авторов, bus-фактор по модулям",
  },
];

const reportTypeHint = computed(
  () => reportTypes.find(rt => rt.value === form.value.report_type)?.hint ?? ""
);

const activeFiltersCount = computed(
  () =>
    [form.value.since, form.value.until, form.value.from_commit, form.value.to_commit]
      .filter(v => v && v.trim() !== "").length
);

const form = ref<AnalyzeRequest>({
  source_type: "local",
  source: "",
  provider: "",
  limit: 0,
  cascade: true,
  diff: true,
  report_type: "decisions",
  since: "",
  until: "",
  from_commit: "",
  to_commit: "",
});

const showFilters = ref(false);

const availableProviders = ref<Provider[]>([]);
const isSubmitting = ref(false);
const error = ref<string | null>(null);

onMounted(async () => {
  try {
    const response = await getProviders();
    availableProviders.value = response.providers;
    form.value.provider = response.default;
  } catch (err) {
    console.error("Failed to load providers:", err);
  }
});

async function handleSubmit() {
  error.value = null;
  isSubmitting.value = true;

  try {
    const response = await analyzeRepository(form.value);
    const job = {
      id: response.job_id,
      status: response.status as any,
      created_at: new Date().toISOString(),
      started_at: new Date().toISOString(),
      finished_at: "",
      request: {
        ...form.value,
        provider: form.value.provider ?? "",
        limit: form.value.limit ?? 0,
        language: "ru",
        cascade: form.value.cascade ?? true,
        diff: form.value.diff ?? true,
      },
    };
    jobsStore.addJob(job);
    router.push("/history");
  } catch (err: any) {
    error.value = err.message || "Произошла ошибка при запуске анализа";
  } finally {
    isSubmitting.value = false;
  }
}
</script>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.3s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
