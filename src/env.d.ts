/// <reference types="astro/client" />
type SiteContext = import('./lib/site.ts').SiteContext

declare namespace App {
  interface Locals {
    site?: SiteContext
    isAdmin?: boolean
  }
}

declare module '@pnfo/singlish-search' {
  export function getPossibleMatches(query: string): string[]
}
