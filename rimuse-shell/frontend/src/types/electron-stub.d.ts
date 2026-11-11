/**
 * Electron type stub.
 *
 * A few ported files import value-less types from 'electron' (e.g.
 * `import { IpcRendererEvent } from 'electron'` in AppLayout). Those imports
 * are type-only and erased at build time, but `tsc` still needs the module to
 * resolve. Instead of installing the whole Electron package (hundreds of MB),
 * declare the minimal surface the ported code references.
 */
declare module "electron" {
    // Only used as a type annotation for IPC event handlers.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    export type IpcRendererEvent = any;
}
