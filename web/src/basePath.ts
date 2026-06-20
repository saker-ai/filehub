import { appBasePath, appURL } from '../../../web-shared/src/base-path'

export function assetHubBasePath(): string {
  return appBasePath(import.meta.env.BASE_URL)
}

export function assetHubURL(path: string): string {
  return appURL(assetHubBasePath(), path)
}
