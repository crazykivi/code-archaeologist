<template>
  <div class="animate-slide-up space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold tracking-tight text-white">История анализов</h1>
        <p class="mt-1.5 text-sm text-gray-500">Управление задачами и просмотр отчётов</p>
      </div>
      <button
        v-if="completedJobs.length > 0"
        @click="clearAll"
        class="btn-ghost text-red-400/70 hover:bg-red-500/[0.06] hover:text-red-400"
      >
        <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
        </svg>
        Очистить завершённые
      </button>
    </div>

    <transition name="fade">
      <div v-if="deleteError" class="glass-card border-red-500/20 bg-red-500/[0.04] p-5">
        <div class="flex items-start gap-3">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-red-500/10">
            <svg class="h-4 w-4 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
            </svg>
          </div>
          <div class="flex-1">
            <p class="text-sm font-medium text-red-300">Не удалось удалить</p>
            <p class="mt-1 text-sm text-red-400/80">{{ deleteError }}</p>
          </div>
          <button @click="deleteError = null" class="text-red-400/60 hover:text-red-300">
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
      </div>
    </transition>

    <div v-if="activeJobs.length > 0" class="glass-card p-6">
      <h2 class="section-title mb-5 flex items-center gap-2">
        <span class="relative flex h-2 w-2">
          <span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-accent-400 opacity-75"></span>
          <span class="relative inline-flex h-2 w-2 rounded-full bg-accent-500"></span>
        </span>
        Активные задачи
      </h2>

      <div class="space-y-4">
        <div
          v-for="job in activeJobs"
          :key="job.id"
          class="glass-card-hover rounded-xl border border-white/[0.04] bg-white/[0.02] p-5"
        >
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0 flex-1">
              <p class="truncate font-mono text-sm font-medium text-gray-200">
                {{ job.request.source }}
              </p>
              <p class="mt-1 text-xs text-gray-500">
                {{ job.request.provider }} · {{ job.request.limit || "все" }} коммитов
              </p>
            </div>
            <span :class="statusBadgeClass(job.status)">{{ statusText(job.status) }}</span>
          </div>

          <div v-if="job.progress" class="mt-5 rounded-xl border border-white/[0.04] bg-white/[0.02] p-4">
            <div class="flex items-center justify-between gap-3 mb-3">
              <span class="text-sm font-medium text-gray-200">{{ job.progress.message }}</span>
              <span class="mono-text shrink-0 rounded-md bg-white/[0.06] px-2 py-0.5 text-gray-400">
                {{ job.progress.stage }}
              </span>
            </div>

            <p v-if="job.progress.details" class="mb-3 truncate font-mono text-xs text-gray-500">
              {{ job.progress.details }}
            </p>

            <template v-if="job.progress.stage === 'extracting_diffs' && job.progress.total_commits">
              <div class="progress-track">
                <div
                  class="progress-bar progress-gradient"
                  :style="{ width: `${(job.progress.processed_items / job.progress.total_commits) * 100}%` }"
                ></div>
              </div>
              <p class="mt-2 text-right text-xs text-gray-500">
                {{ job.progress.processed_items }} / {{ job.progress.total_commits }}
              </p>
            </template>

            <template v-else-if="job.progress.stage === 'analyzing_map' && job.progress.total_batches">
              <div class="progress-track">
                <div
                  class="progress-bar progress-gradient"
                  :style="{ width: `${(job.progress.done_batches / job.progress.total_batches) * 100}%` }"
                ></div>
              </div>
              <p class="mt-2 text-right text-xs text-gray-500">
                {{ job.progress.done_batches }} / {{ job.progress.total_batches }}
              </p>
            </template>

            <template v-else-if="job.progress.stage === 'analyzing_reduce' && job.progress.total_reduce">
              <div class="progress-track">
                <div
                  class="progress-bar progress-gradient"
                  :style="{ width: `${(job.progress.done_reduce / job.progress.total_reduce) * 100}%` }"
                ></div>
              </div>
              <p class="mt-2 text-right text-xs text-gray-500">
                {{ job.progress.done_reduce }} / {{ job.progress.total_reduce }}
              </p>
            </template>

            <div v-else class="flex items-center gap-1.5 pt-1">
              <div class="loading-dot"></div>
              <div class="loading-dot"></div>
              <div class="loading-dot"></div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div v-if="completedJobs.length > 0" class="glass-card p-6">
      <h2 class="section-title mb-5">Завершённые задачи</h2>

      <div class="space-y-3">
        <div
          v-for="job in completedJobs"
          :key="job.id"
          class="glass-card-hover group rounded-xl border border-white/[0.04] bg-white/[0.02] p-5"
        >
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0 flex-1">
              <p class="truncate font-mono text-sm font-medium text-gray-200">
                {{ job.request.source }}
              </p>
              <p class="mt-1 text-xs text-gray-500">
                {{ job.request.provider }} · {{ job.request.limit || "все" }} коммитов ·
                {{ formatDate(job.created_at) }}
              </p>
            </div>

            <div class="flex items-center gap-2">
              <span :class="statusBadgeClass(job.status)">{{ statusText(job.status) }}</span>
              <button
                @click="deleteJob(job.id)"
                :disabled="deletingJobs.has(job.id)"
                class="flex h-7 w-7 items-center justify-center rounded-lg text-gray-600 opacity-0 transition-all hover:bg-red-500/10 hover:text-red-400 group-hover:opacity-100 disabled:opacity-50 disabled:cursor-not-allowed"
                :aria-label="`Удалить задачу ${job.id}`"
              >
                <svg v-if="deletingJobs.has(job.id)" class="h-3.5 w-3.5 animate-spin" viewBox="0 0 24 24" fill="none">
                  <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
                  <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                <svg v-else class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
          </div>

          <div v-if="job.status === 'completed' && job.report_id" class="mt-4 flex items-center gap-3">
            <button @click="viewReport(job.report_id!)" class="btn-primary !px-4 !py-2 text-xs">
              <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
              </svg>
              Отчёт
            </button>
            <button @click="downloadReport(job.report_id!, job.request.source)" class="btn-secondary !px-4 !py-2 text-xs">
              <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
              </svg>
              Markdown
            </button>
          </div>

          <div v-if="job.status === 'failed' && job.error" class="mt-3 flex items-center gap-2">
            <svg class="h-4 w-4 shrink-0 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <p class="text-sm text-red-400/80">{{ job.error }}</p>
          </div>
        </div>
      </div>
    </div>

    <div v-if="activeJobs.length === 0 && completedJobs.length === 0" class="flex flex-col items-center justify-center py-24">
      <div class="mb-6 flex h-16 w-16 items-center justify-center rounded-2xl bg-white/[0.03]">
        <svg class="h-8 w-8 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
        </svg>
      </div>
      <p class="mb-1 text-sm font-medium text-gray-400">Нет задач</p>
      <p class="mb-6 text-sm text-gray-600">Запустите первый анализ репозитория</p>
      <router-link to="/" class="btn-primary">
        <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
        </svg>
        Новый анализ
      </router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import { useJobsStore } from "@/stores/jobs";
import { getReport, deleteJobApi } from "@/services/api";

const router = useRouter();
const jobsStore = useJobsStore();

const activeJobs = computed(() => jobsStore.activeJobs);
const completedJobs = computed(() => jobsStore.completedJobs);

const deletingJobs = ref<Set<string>>(new Set());
const deleteError = ref<string | null>(null);

function statusBadgeClass(status: string) {
  switch (status) {
    case "queued":
      return "badge-queued";
    case "running":
      return "badge-running";
    case "completed":
      return "badge-completed";
    case "failed":
      return "badge-failed";
    default:
      return "badge bg-gray-500/10 text-gray-400 ring-1 ring-gray-500/20";
  }
}

function statusText(status: string) {
  switch (status) {
    case "queued": return "В очереди";
    case "running": return "Выполняется";
    case "completed": return "Завершено";
    case "failed": return "Ошибка";
    default: return status;
  }
}

function formatDate(dateString: string) {
  return new Date(dateString).toLocaleString("ru-RU");
}

function viewReport(reportId: string) {
  router.push(`/report/${reportId}`);
}

async function downloadReport(reportId: string, source: string) {
  try {
    const report = await getReport(reportId);
    const blob = new Blob([report.markdown], { type: "text/markdown" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `report-${source.split("/").pop()}-${new Date().toISOString().split("T")[0]}.md`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  } catch (err) {
    console.error("Failed to download report:", err);
  }
}

async function deleteJob(id: string) {
  if (deletingJobs.value.has(id)) return;
  deleteError.value = null;
  deletingJobs.value.add(id);

  try {
    await deleteJobApi(id);
    jobsStore.clearJob(id);
  } catch (err: any) {
    deleteError.value = err.message || "Не удалось удалить задачу";
  } finally {
    deletingJobs.value.delete(id);
  }
}

async function clearAll() {
  deleteError.value = null;
  const toDelete = jobsStore.completedJobs.map(j => j.id);
  for (const id of toDelete) {
    try {
      deletingJobs.value.add(id);
      await deleteJobApi(id);
      jobsStore.clearJob(id);
    } catch (err: any) {
      console.error(`Failed to delete job ${id}:`, err);
    } finally {
      deletingJobs.value.delete(id);
    }
  }
}
</script>

<style scoped>
.fade-enter-active, .fade-leave-active {
  transition: opacity 0.3s ease;
}
.fade-enter-from, .fade-leave-to {
  opacity: 0;
}
</style>