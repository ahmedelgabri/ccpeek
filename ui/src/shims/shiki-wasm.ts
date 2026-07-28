// Build-time stand-in for `shiki/wasm` (see ui/vite.config.ts). The only
// reference is a dynamic import inside @pierre/diffs' shared highlighter,
// on the wasm-engine branch this app never takes — but the bare import
// expression is enough to make the bundler emit the ~620 kB wasm payload
// into the embedded assets. If this default is ever read, the shiki shim's
// createOnigurumaEngine has already thrown.
export default undefined;
