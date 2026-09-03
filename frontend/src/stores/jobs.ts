import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Job } from '@/types'
import { getJob } from '@/services/api'

function isTerminal(status: Job['status']): boolean {
  return status === 'completed' || status === 'failed'
}

export const useJobsStore = defineStore('jobs', () => {
    const jobs = ref<Map<string, Job>>(new Map())
    const pollingInterval = ref<number | null>(null)
    const watchers = ref<Map<string, EventSource>>(new Map())

    const activeJobs = computed(() =>
        Array.from(jobs.value.values()).filter(j => j.status === 'queued' || j.status === 'running')
    )

    const completedJobs = computed(() =>
        Array.from(jobs.value.values())
            .filter(j => j.status === 'completed' || j.status === 'failed')
            .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    )

    function addJob(job: Job) {
        jobs.value.set(job.id, job)
        saveToStorage()
        startWatching(job.id)
    }

    function updateJob(job: Job) {
        jobs.value.set(job.id, job)
        saveToStorage()
        if (isTerminal(job.status)) {
            stopWatching(job.id)
        }
    }

    async function refreshJob(id: string) {
        try {
            const job = await getJob(id)
            updateJob(job)
            return job
        } catch (error) {
            console.error(`Failed to refresh job ${id}:`, error)
            return null
        }
    }

    // --- SSE: живой прогресс, polling остаётся фолбэком ---

    function startWatching(id: string) {
        if (watchers.value.has(id)) return

        const es = new EventSource(`/api/v1/jobs/${id}/events`)
        watchers.value.set(id, es)

        es.onmessage = (event: MessageEvent) => {
            try {
                const payload = JSON.parse(event.data) as {
                    type: string
                    status?: Job['status']
                    progress?: Job['progress']
                    report_id?: string
                    error?: string
                }

                const current = jobs.value.get(id)
                if (!current) {
                    stopWatching(id)
                    return
                }

                if (payload.type === 'progress') {
                    updateJob({ ...current, status: 'running', progress: payload.progress ?? current.progress })
                } else if (payload.type === 'status' || payload.type === 'snapshot') {
                    const status = payload.status ?? current.status
                    updateJob({
                        ...current,
                        status,
                        progress: payload.progress ?? current.progress,
                        report_id: payload.report_id ?? current.report_id,
                        error: payload.error ?? current.error
                    })
                    if (isTerminal(status)) {
                        stopWatching(id)
                        refreshJob(id)
                    }
                }
            } catch (err) {
                console.warn('Failed to parse SSE payload:', err)
            }
        }

        es.onerror = () => {
            // Сервер недоступен или соединение оборвалось — переходим на polling.
            stopWatching(id)
            startPolling()
        }
    }

    function stopWatching(id: string) {
        const es = watchers.value.get(id)
        if (es) {
            es.close()
            watchers.value.delete(id)
        }
    }

    function stopAllWatching() {
        for (const [id] of watchers.value) {
            stopWatching(id)
        }
    }

    // --- Polling fallback ---

    function startPolling() {
        if (pollingInterval.value) return

        pollingInterval.value = window.setInterval(async () => {
            const active = activeJobs.value
            if (active.length === 0) {
                stopPolling()
                return
            }

            for (const job of active) {
                await refreshJob(job.id)
            }
        }, 3000)
    }

    function stopPolling() {
        if (pollingInterval.value) {
            clearInterval(pollingInterval.value)
            pollingInterval.value = null
        }
    }

    // --- Persistence ---

    let saveTimer: number | null = null

    function saveToStorage() {
        if (saveTimer !== null) return
        saveTimer = window.setTimeout(() => {
            saveTimer = null
            try {
                const jobsArray = Array.from(jobs.value.values())
                localStorage.setItem('jobs', JSON.stringify(jobsArray))
            } catch (err) {
                console.warn('Failed to persist jobs:', err)
            }
        }, 500)
    }

    async function loadFromStorage() {
        try {
            const { listJobs } = await import('@/services/api')
            const jobsArray = await listJobs()
            jobs.value = new Map(jobsArray.map(j => [j.id, j]))
        } catch (err) {
            console.warn('Failed to load jobs from backend, falling back to localStorage:', err)
            const stored = localStorage.getItem('jobs')
            if (stored) {
                try {
                    const jobsArray = JSON.parse(stored) as Job[]
                    jobs.value = new Map(jobsArray.map(j => [j.id, j]))
                } catch (parseErr) {
                    console.warn('Failed to parse stored jobs:', parseErr)
                    localStorage.removeItem('jobs')
                }
            }
        }

        for (const job of activeJobs.value) {
            startWatching(job.id)
        }
        if (activeJobs.value.length > 0) {
            startPolling()
        }
    }

    function clearJob(id: string) {
        stopWatching(id)
        jobs.value.delete(id)
        saveToStorage()
    }

    function clearAllCompleted() {
        for (const [id, job] of jobs.value.entries()) {
            if (isTerminal(job.status)) {
                jobs.value.delete(id)
            }
        }
        saveToStorage()
    }

    loadFromStorage()

    return {
        jobs,
        activeJobs,
        completedJobs,
        addJob,
        updateJob,
        refreshJob,
        clearJob,
        clearAllCompleted,
        stopAllWatching
    }
})
