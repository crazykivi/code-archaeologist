<template>
  <div class="animate-slide-up space-y-6">
    <div class="flex items-center justify-between">
      <button @click="goBack" class="btn-secondary !px-4 !py-2">
        <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
        </svg>
        Назад
      </button>
      <button @click="downloadReport" class="btn-primary !px-4 !py-2">
        <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
        </svg>
        Скачать .md
      </button>
    </div>

    <div v-if="isLoading" class="glass-card flex flex-col items-center justify-center p-20">
      <div class="relative mb-6">
        <div class="h-12 w-12 rounded-full border-2 border-white/[0.06] border-t-accent-500 animate-spin"></div>
      </div>
      <p class="text-sm text-gray-500">Загрузка отчёта…</p>
    </div>

    <div v-else-if="error" class="glass-card border-red-500/20 bg-red-500/[0.04] p-6">
      <div class="flex items-start gap-3">
        <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-red-500/10">
          <svg class="h-4 w-4 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
          </svg>
        </div>
        <div>
          <p class="text-sm font-medium text-red-300">Ошибка загрузки</p>
          <p class="mt-1 text-sm text-red-400/80">{{ error }}</p>
        </div>
      </div>
    </div>

    <div v-else class="glass-card p-8 sm:p-10">
      <div class="prose-modern" v-html="renderedMarkdown"></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMarkdown } from '@/composables/useMarkdown'
import { getReport } from '@/services/api'

const route = useRoute()
const router = useRouter()
const { renderMarkdown } = useMarkdown()

const isLoading = ref(true)
const error = ref<string | null>(null)
const markdown = ref('')
const renderedMarkdown = ref('')

onMounted(async () => {
  const reportId = route.params.id as string
  try {
    const report = await getReport(reportId)
    markdown.value = report.markdown
    renderedMarkdown.value = await renderMarkdown(markdown.value)
  } catch (err: any) {
    error.value = err.message || 'Не удалось загрузить отчёт'
  } finally {
    isLoading.value = false
  }
})

function goBack() {
  router.push('/history')
}

function downloadReport() {
  const blob = new Blob([markdown.value], { type: 'text/markdown' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `report-${new Date().toISOString().split('T')[0]}.md`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}
</script>