import { getAPIKey, setAPIKey } from '../api/client'

export function useAuth() {
  return { apiKey: getAPIKey(), setAPIKey }
}
