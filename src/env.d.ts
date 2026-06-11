/// <reference types="astro/client" />

declare namespace App {
  interface Locals {
    scope: string | null;
    adminEmail?: string;
  }
}
