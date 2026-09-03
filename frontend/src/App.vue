<template>
  <div class="min-h-screen">
    <div class="ambient-bg"></div>

    <nav class="sticky top-0 z-50 border-b border-white/[0.04] bg-surface-950/80 backdrop-blur-xl">
      <div class="mx-auto max-w-6xl px-6">
        <div class="flex h-16 items-center justify-between">
          <div class="flex items-center gap-8">
            <router-link to="/" class="group flex items-center gap-2.5">
              <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-accent-500 to-glow-violet text-sm shadow-lg shadow-accent-500/20">
                <span class="text-white">⛏</span>
              </div>
              <span class="text-[15px] font-bold tracking-tight text-white">
                Code Archaeologist
              </span>
            </router-link>

            <div class="hidden items-center gap-1 sm:flex">
              <router-link
                to="/"
                class="rounded-lg px-3.5 py-2 text-sm font-medium transition-all duration-200"
                :class="$route.path === '/'
                  ? 'bg-accent-500/10 text-accent-400'
                  : 'text-gray-400 hover:bg-white/[0.04] hover:text-gray-200'"
              >
                Анализ
              </router-link>
              <router-link
                to="/history"
                class="flex items-center gap-2 rounded-lg px-3.5 py-2 text-sm font-medium transition-all duration-200"
                :class="$route.path === '/history'
                  ? 'bg-accent-500/10 text-accent-400'
                  : 'text-gray-400 hover:bg-white/[0.04] hover:text-gray-200'"
              >
                История
                <span
                  v-if="activeJobsCount > 0"
                  class="flex h-5 min-w-5 items-center justify-center rounded-full bg-accent-500 px-1.5 text-[11px] font-semibold text-white"
                >
                  {{ activeJobsCount }}
                </span>
              </router-link>
              <router-link
                to="/settings"
                class="flex items-center gap-2 rounded-lg px-3.5 py-2 text-sm font-medium transition-all duration-200"
                :class="$route.path === '/settings'
                  ? 'bg-accent-500/10 text-accent-400'
                  : 'text-gray-400 hover:bg-white/[0.04] hover:text-gray-200'"
              >
                Настройки
              </router-link>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button
              @click="toggleTheme"
              class="flex h-9 w-9 items-center justify-center rounded-lg text-gray-400 transition-all duration-200 hover:bg-white/[0.06] hover:text-gray-200"
            >
              <span class="text-base">{{ isDark ? '☀️' : '🌙' }}</span>
            </button>
          </div>
        </div>
      </div>
    </nav>

    <main class="mx-auto max-w-6xl px-6 py-8">
      <router-view v-slot="{ Component }">
        <transition name="page" mode="out-in">
          <component :is="Component" />
        </transition>
      </router-view>
    </main>

    <footer class="border-t border-white/[0.03] py-6 text-center">
      <p class="text-xs text-gray-600">Code Archaeologist — анализ истории Git-репозиториев</p>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useJobsStore } from '@/stores/jobs'

const jobsStore = useJobsStore()
const isDark = computed(() => document.documentElement.classList.contains('dark'))
const activeJobsCount = computed(() => jobsStore.activeJobs.length)

function toggleTheme() {
  document.documentElement.classList.toggle('dark')
  localStorage.setItem('theme', document.documentElement.classList.contains('dark') ? 'dark' : 'light')
}

if (localStorage.theme === 'dark' || (!('theme' in localStorage) && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
  document.documentElement.classList.add('dark')
} else {
  document.documentElement.classList.remove('dark')
}
</script>

<style scoped>
.page-enter-active,
.page-leave-active {
  transition: opacity 0.2s ease, transform 0.2s ease;
}

.page-enter-from {
  opacity: 0;
  transform: translateY(8px);
}

.page-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>