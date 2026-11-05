<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{
  enabled: boolean
  profileId: number | null
  profiles: { id: number; name: string }[]
  hint: string
}>()

const emit = defineEmits<{
  'update:enabled': [value: boolean]
  'update:profileId': [value: number | null]
}>()

const { t } = useI18n()
</script>

<template>
  <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
    <div class="flex items-center justify-between">
      <div>
        <label class="input-label mb-0">{{ t('admin.accounts.quotaControl.tlsFingerprint.label') }}</label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ hint }}</p>
      </div>
      <button
        type="button"
        data-testid="tls-fingerprint-toggle"
        @click="emit('update:enabled', !enabled)"
        :class="[
          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
          enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
        ]"
      >
        <span
          :class="[
            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
            enabled ? 'translate-x-5' : 'translate-x-0'
          ]"
        />
      </button>
    </div>
    <div v-if="enabled" class="mt-3">
      <select
        :value="profileId"
        class="input"
        @change="emit('update:profileId', ($event.target as HTMLSelectElement).value === '' ? null : Number(($event.target as HTMLSelectElement).value))"
      >
        <option value="">{{ t('admin.accounts.quotaControl.tlsFingerprint.defaultProfile') }}</option>
        <option v-if="profiles.length > 0" :value="-1">{{ t('admin.accounts.quotaControl.tlsFingerprint.randomProfile') }}</option>
        <option v-for="p in profiles" :key="p.id" :value="p.id">{{ p.name }}</option>
      </select>
    </div>
  </div>
</template>
