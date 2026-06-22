import { appBasePath, appURL } from '@saker/web-shared/base-path'

export function assetHubBasePath(): string {
  return appBasePath(import.meta.env.BASE_URL)
}

export function assetHubURL(path: string): string {
  return appURL(assetHubBasePath(), path)
}
