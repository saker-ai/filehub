import { getAPIKey, setAPIKey } from '@saker/filehub-client'

export function useAuth() {
  return { apiKey: getAPIKey(), setAPIKey }
}
