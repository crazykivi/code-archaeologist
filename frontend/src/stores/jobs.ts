import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Job } from '@/types'
import { getJob } from '@/services/api'

export const useJobsStore = defineStore('jobs', () => {
    const jobs = ref<Map<string, Job>>(new Map())
    const pollingInterval = ref<number | null>(null)

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
        startPolling()
    }

    function updateJob(job: Job) {
        jobs.value.set(job.id, job)
        saveToStorage()
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

    function saveToStorage() {
        const jobsArray = Array.from(jobs.value.values())
        localStorage.setItem('jobs', JSON.stringify(jobsArray))
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
                const jobsArray = JSON.parse(stored) as Job[]
                jobs.value = new Map(jobsArray.map(j => [j.id, j]))
            }
        }

        if (activeJobs.value.length > 0) {
            startPolling()
        }
    }

    function clearJob(id: string) {
        jobs.value.delete(id)
        saveToStorage()
    }

    function clearAllCompleted() {
        for (const [id, job] of jobs.value.entries()) {
            if (job.status === 'completed' || job.status === 'failed') {
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
        clearAllCompleted
    }
})