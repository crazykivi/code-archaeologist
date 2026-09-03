<template>
  <div class="animate-slide-up space-y-6">
    <div class="mb-8">
      <h1 class="text-2xl font-bold tracking-tight text-white">Настройки провайдеров</h1>
      <p class="mt-1.5 text-sm text-gray-500">
        Сохранённые настройки хранятся в БД и перекрывают значения из .env
      </p>
    </div>

    <transition name="fade">
      <div v-if="error" class="glass-card border-red-500/20 bg-red-500/[0.04] p-5">
        <div class="flex items-start gap-3">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-red-500/10">
            <svg class="h-4 w-4 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
            </svg>
          </div>
          <div class="flex-1">
            <p class="text-sm font-medium text-red-300">Ошибка</p>
            <p class="mt-1 text-sm text-red-400/80">{{ error }}</p>
          </div>
          <button @click="error = null" class="text-red-400/60 hover:text-red-300" aria-label="Закрыть">
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>
    </transition>

    <div v-if="isLoading" class="glass-card p-10 text-center text-sm text-gray-500">
      Загрузка…
    </div>

    <div v-else class="space-y-4">
      <div
        v-for="p in providers"
        :key="p.name"
        class="glass-card p-6"
      >
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-mono text-sm font-semibold text-gray-100">{{ p.name }}</span>
              <span v-if="p.custom" class="badge bg-violet-500/10 text-violet-300 ring-1 ring-violet-500/20">свой</span>
              <span v-if="p.overridden" class="badge bg-accent-500/10 text-accent-400 ring-1 ring-accent-500/20">переопределён</span>
              <span :class="p.configured ? 'badge-completed' : 'badge-failed'">
                {{ p.configured ? "готов" : "не настроен" }}
              </span>
            </div>
            <p class="mt-2 truncate font-mono text-xs text-gray-500">
              {{ p.base_url || "base URL не задан" }}
            </p>
            <p class="mt-1 text-xs text-gray-500">
              Модель: <span class="font-mono text-gray-400">{{ p.model || "—" }}</span>
              · Ключ:
              <span :class="p.api_key_set ? 'text-emerald-400' : 'text-gray-500'">
                {{ p.api_key_set ? "установлен" : "нет" }}
              </span>
              <template v-if="p.headers && Object.keys(p.headers).length">
                · Заголовки: <span class="font-mono text-gray-400">{{ Object.keys(p.headers).join(", ") }}</span>
              </template>
            </p>
          </div>

          <div class="flex shrink-0 items-center gap-2">
            <button
              @click="test(p.name)"
              :disabled="testing.has(p.name)"
              class="btn-secondary !px-4 !py-2 text-xs"
            >
              {{ testing.has(p.name) ? "Проверка…" : "Проверить" }}
            </button>
            <button @click="startEdit(p)" class="btn-secondary !px-4 !py-2 text-xs">
              Настроить
            </button>
            <button
              v-if="p.custom || p.overridden"
              @click="remove(p.name)"
              :disabled="deleting.has(p.name)"
              class="btn-ghost text-red-400/70 hover:bg-red-500/[0.06] hover:text-red-400 !px-4 !py-2 text-xs"
            >
              {{ p.custom ? "Удалить" : "Сбросить" }}
            </button>
          </div>
        </div>

        <div
          v-if="testResults[p.name]"
          :class="[
            'mt-4 rounded-xl border p-4 text-sm',
            testResults[p.name].ok
              ? 'border-emerald-500/20 bg-emerald-500/[0.04] text-emerald-300'
              : 'border-red-500/20 bg-red-500/[0.04] text-red-300',
          ]"
          role="status"
          aria-live="polite"
        >
          <template v-if="testResults[p.name].ok">
            Соединение работает. Ответ модели: «{{ testResults[p.name].reply }}»
          </template>
          <template v-else>
            {{ testResults[p.name].error }}
          </template>
        </div>

        <form
          v-if="editing === p.name"
          @submit.prevent="save(p.name)"
          class="mt-5 space-y-4 rounded-xl border border-white/[0.04] bg-white/[0.02] p-4"
        >
          <div class="grid gap-4 sm:grid-cols-2">
            <div class="space-y-2">
              <label class="text-xs text-gray-400">Base URL</label>
              <input v-model="draft.base_url" type="text" placeholder="https://api.example.com/v1" class="input-field font-mono text-[13px]" />
            </div>
            <div class="space-y-2">
              <label class="text-xs text-gray-400">Модель</label>
              <input v-model="draft.model" type="text" placeholder="имя модели" class="input-field font-mono text-[13px]" />
            </div>
          </div>

          <div class="space-y-2">
            <label class="text-xs text-gray-400">
              API-ключ <span v-if="p.api_key_set" class="text-gray-600">(оставьте пустым, чтобы не менять)</span>
            </label>
            <input v-model="draft.api_key" type="password" autocomplete="off" placeholder="••••••" class="input-field font-mono text-[13px]" />
          </div>

          <div class="space-y-2">
            <div class="flex items-center justify-between">
              <label class="text-xs text-gray-400">Заголовки запросов</label>
              <button type="button" @click="addHeader" class="btn-ghost !px-3 !py-1 text-xs">
                + Заголовок
              </button>
            </div>
            <p class="text-xs text-gray-600">
              Можно задать свой формат авторизации: значение <code class="font-mono">{{ apiKeyPlaceholder }}</code> заменяется на реальный ключ. Если заголовок Authorization не задан, отправляется «Bearer ключ».
            </p>
            <div
              v-for="(h, i) in draft.headers"
              :key="i"
              class="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto] items-center gap-2"
            >
              <input v-model="h.key" type="text" placeholder="Имя" class="input-field font-mono text-[13px]" />
              <input v-model="h.value" type="text" placeholder="Значение" class="input-field font-mono text-[13px]" autocomplete="off" />
              <button
                type="button"
                @click="draft.headers.splice(i, 1)"
                class="flex h-8 w-8 items-center justify-center rounded-lg text-gray-500 hover:bg-red-500/10 hover:text-red-400"
                aria-label="Удалить заголовок"
              >
                <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
          </div>

          <div class="flex items-center justify-end gap-2 border-t border-white/[0.04] pt-4">
            <button type="button" @click="editing = null" class="btn-ghost !px-4 !py-2 text-xs">Отмена</button>
            <button type="submit" :disabled="saving" class="btn-primary !px-4 !py-2 text-xs">
              {{ saving ? "Сохранение…" : "Сохранить" }}
            </button>
          </div>
        </form>
      </div>
    </div>

    <div class="glass-card p-6">
      <h2 class="section-title mb-4">Добавить провайдера</h2>
      <form @submit.prevent="create" class="flex flex-wrap items-center gap-3">
        <input
          v-model="newName"
          type="text"
          placeholder="имя: латиница, цифры, дефис"
          class="input-field max-w-xs font-mono text-[13px]"
          required
        />
        <button type="submit" :disabled="saving" class="btn-primary !px-4 !py-2 text-xs">
          Создать
        </button>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import {
  getProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  testProvider,
} from "@/services/api";
import type { Provider } from "@/types";

const providers = ref<Provider[]>([]);
const isLoading = ref(true);
const saving = ref(false);
const error = ref<string | null>(null);
const editing = ref<string | null>(null);
const deleting = ref<Set<string>>(new Set());
const testing = ref<Set<string>>(new Set());
const testResults = ref<Record<string, { ok: boolean; reply?: string; error?: string }>>({});
const newName = ref("");

const apiKeyPlaceholder = "{{api_key}}";

const draft = ref({
  base_url: "",
  model: "",
  api_key: "",
  headers: [] as { key: string; value: string }[],
});

onMounted(load);

async function load() {
  isLoading.value = true;
  try {
    const response = await getProviders();
    providers.value = response.providers;
  } catch (err: any) {
    error.value = err.message || "Не удалось загрузить провайдеров";
  } finally {
    isLoading.value = false;
  }
}

function startEdit(p: Provider) {
  editing.value = p.name;
  draft.value = {
    base_url: p.base_url ?? "",
    model: p.model ?? "",
    api_key: "",
    headers: Object.entries(p.headers ?? {}).map(([key, value]) => ({ key, value })),
  };
}

function addHeader() {
  draft.value.headers.push({ key: "", value: "" });
}

async function test(name: string) {
  if (testing.value.has(name)) return;
  error.value = null;
  testing.value.add(name);
  delete testResults.value[name];
  try {
    testResults.value[name] = await testProvider(name);
  } catch (err: any) {
    testResults.value[name] = { ok: false, error: err.message || "Проверка не удалась" };
  } finally {
    testing.value.delete(name);
  }
}

function headersPayload(): Record<string, string> {
  const out: Record<string, string> = {};
  for (const h of draft.value.headers) {
    const key = h.key.trim();
    if (key) out[key] = h.value;
  }
  return out;
}

async function save(name: string) {
  error.value = null;
  saving.value = true;
  try {
    const payload: Parameters<typeof updateProvider>[1] = {
      base_url: draft.value.base_url,
      model: draft.value.model,
      headers: headersPayload(),
    };
    if (draft.value.api_key.trim() !== "") {
      payload.api_key = draft.value.api_key;
    }
    await updateProvider(name, payload);
    editing.value = null;
    await load();
  } catch (err: any) {
    error.value = err.message || "Не удалось сохранить настройки";
  } finally {
    saving.value = false;
  }
}

async function create() {
  const name = newName.value.trim().toLowerCase();
  if (!name) return;
  error.value = null;
  saving.value = true;
  try {
    await createProvider(name, { base_url: "", model: "" });
    newName.value = "";
    await load();
    const created = providers.value.find(p => p.name === name);
    if (created) startEdit(created);
  } catch (err: any) {
    error.value = err.message || "Не удалось создать провайдера";
  } finally {
    saving.value = false;
  }
}

async function remove(name: string) {
  if (deleting.value.has(name)) return;
  error.value = null;
  deleting.value.add(name);
  try {
    await deleteProvider(name);
    if (editing.value === name) editing.value = null;
    await load();
  } catch (err: any) {
    error.value = err.message || "Не удалось удалить";
  } finally {
    deleting.value.delete(name);
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
