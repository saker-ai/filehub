import { getAPIKey, setAPIKey } from '@saker/assethub-client'

export function useAuth() {
  return { apiKey: getAPIKey(), setAPIKey }
}
