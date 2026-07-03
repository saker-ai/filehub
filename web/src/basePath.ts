import { appBasePath, appURL } from '@saker/web-shared/base-path'

export function fileHubBasePath(): string {
  return appBasePath(import.meta.env.BASE_URL)
}

export function fileHubURL(path: string): string {
  return appURL(fileHubBasePath(), path)
}
