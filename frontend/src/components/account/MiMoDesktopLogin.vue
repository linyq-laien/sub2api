<template>
  <section class="space-y-3 rounded-lg border border-primary-200 p-4 dark:border-primary-800" data-testid="mimo-desktop-login">
    <div class="font-medium">{{ t('admin.accounts.mimo.loginTitle') }}</div>
    <p class="input-hint">{{ t('admin.accounts.mimo.loginHint') }}</p>
    <button type="button" class="btn btn-primary" :disabled="starting" @click="start">
      {{ t(starting ? 'admin.accounts.mimo.loginStarting' : 'admin.accounts.mimo.loginStart') }}
    </button>
    <div v-if="status === 'pending' && loginURL" class="flex flex-wrap items-center gap-4">
      <img v-if="qrImage" :src="qrImage" :alt="t('admin.accounts.mimo.loginQR')" class="h-44 w-44 rounded bg-white" />
      <div class="space-y-2">
        <a :href="loginURL" target="_blank" rel="noopener noreferrer" class="text-primary-600 underline">{{ t('admin.accounts.mimo.loginOpen') }}</a>
        <p class="input-hint">{{ t('admin.accounts.mimo.loginWaiting') }}</p>
      </div>
    </div>
    <p v-if="status === 'authenticated'" role="status" class="text-sm text-emerald-600">{{ t('admin.accounts.mimo.loginSuccess') }}</p>
    <p v-if="status === 'failed' || status === 'expired'" role="alert" class="text-sm text-red-600">{{ t(`admin.accounts.mimo.login${status === 'expired' ? 'Expired' : 'Failed'}`) }}</p>
    <details>
      <summary class="cursor-pointer text-sm text-gray-500">{{ t('admin.accounts.mimo.loginManual') }}</summary>
      <input type="password" :value="cookie" autocomplete="new-password" class="input mt-2 font-mono" placeholder="serviceToken=...; userId=..." @input="manualCookie" />
    </details>
  </section>
</template>

<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import { apiClient } from '@/api/client'

defineProps<{ modelValue: string; cookie: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; 'update:cookie': [value: string] }>()
const { t } = useI18n()
const starting = ref(false)
const status = ref('')
const loginURL = ref('')
const qrImage = ref('')
let sessionID = ''
let generation = 0
let timer: ReturnType<typeof setTimeout> | undefined
let expiryTimer: ReturnType<typeof setTimeout> | undefined
const path = '/admin/accounts/mimo/desktop/login'
interface LoginStatus { session_id: string; status: string; login_url?: string; expires_at: string }

function discard() {
  generation++
  clearTimeout(timer)
  clearTimeout(expiryTimer)
  const previous = sessionID
  sessionID = ''
  if (previous) void apiClient.delete(`${path}/${previous}`).catch(() => {})
}
async function poll(version: number) {
  try {
    const { data } = await apiClient.get<LoginStatus>(`${path}/${sessionID}`)
    if (version !== generation) return
    status.value = data.status
    if (data.status === 'authenticated') {
      emit('update:cookie', '')
      emit('update:modelValue', data.session_id)
      loginURL.value = ''
      qrImage.value = ''
    } else if (data.status === 'pending') {
      timer = setTimeout(() => void poll(version), 1500)
    }
  } catch {
    if (version === generation) status.value = 'failed'
  }
}
async function start() {
  discard()
  emit('update:modelValue', '')
  const version = generation
  starting.value = true
  status.value = ''
  loginURL.value = ''
  qrImage.value = ''
  try {
    const { data } = await apiClient.post<LoginStatus>(path)
    if (version !== generation) {
      void apiClient.delete(`${path}/${data.session_id}`).catch(() => {})
      return
    }
    sessionID = data.session_id
    loginURL.value = data.login_url || ''
    status.value = data.status
    const image = await QRCode.toDataURL(loginURL.value, { width: 240, margin: 2 })
    if (version !== generation) return
    qrImage.value = image
    expiryTimer = setTimeout(() => {
      discard()
      emit('update:modelValue', '')
      status.value = 'expired'
    }, Math.max(0, Date.parse(data.expires_at) - Date.now()))
    timer = setTimeout(() => void poll(version), 1500)
  } catch {
    if (version === generation) status.value = 'failed'
  } finally {
    if (version === generation) starting.value = false
  }
}
function manualCookie(event: Event) {
  discard()
  starting.value = false
  status.value = ''
  emit('update:modelValue', '')
  emit('update:cookie', (event.target as HTMLInputElement).value)
}
onBeforeUnmount(discard)
</script>
