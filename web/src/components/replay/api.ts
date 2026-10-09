import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import {
  getReplayData,
  getReplaySettings,
  getReplayStorage,
  listReplays,
  putReplaySettings,
  putReplayStorage,
  ReplayApiError,
  type ReplayStorageSettings,
  type SiteSettings,
} from '@/lib/replay-api'

export const replayKeys = {
  settings: (siteId: number) => ['replay', siteId, 'settings'] as const,
  list: (siteId: number, from: string, to: string, cursor?: string) => ['replay', siteId, 'list', { from, to, cursor }] as const,
  data: (siteId: number, sessionId: string) => ['replay', siteId, 'data', sessionId] as const,
  storage: ['replay', 'storage'] as const,
}

export function useReplayAvailability(siteId?: number) {
  const query = useQuery({
    queryKey: replayKeys.settings(siteId ?? 0),
    queryFn: ({ signal }) => getReplaySettings(siteId!, signal),
    enabled: Boolean(siteId),
    retry: (count, error) => !(error instanceof ReplayApiError && error.status === 404) && count < 1,
  })
  // On only when SUPERMETRY_REPLAY_ENABLED is set; the settings tab still loads to show "Off".
  return { ...query, enabled: query.isSuccess && query.data.enabled }
}

export function useReplaySettings(siteId: number) {
  return useQuery({ queryKey: replayKeys.settings(siteId), queryFn: ({ signal }) => getReplaySettings(siteId, signal) })
}

export function useSaveReplaySettings(siteId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (settings: SiteSettings) => putReplaySettings(siteId, settings),
    onSuccess: (settings) => client.setQueryData(replayKeys.settings(siteId), settings),
  })
}

export function useReplays(siteId: number, from: string, to: string, cursor?: string) {
  return useQuery({
    queryKey: replayKeys.list(siteId, from, to, cursor),
    queryFn: ({ signal }) => listReplays(siteId, { from, to, cursor, limit: 50 }, signal),
  })
}

export function useReplayData(siteId: number, sessionId: string) {
  return useQuery({
    queryKey: replayKeys.data(siteId, sessionId),
    queryFn: ({ signal }) => getReplayData(siteId, sessionId, signal),
  })
}

export function useReplayStorage() {
  return useQuery({ queryKey: replayKeys.storage, queryFn: ({ signal }) => getReplayStorage(signal) })
}

export function useSaveReplayStorage() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ settings, migrate }: { settings: ReplayStorageSettings; migrate: boolean }) => putReplayStorage(settings, migrate),
    onSuccess: (settings) => client.setQueryData(replayKeys.storage, settings),
  })
}
