// formatScript pretty-prints a model-written script (Pi's codemode) for
// reading. Models emit these as dense one-liners — several tool calls
// crammed inside one Promise.allSettled — which nobody can scan as stored.
// Prettier loads as its own chunk the first time a script is opened, so
// pages that never show one never pay for it. A script that does not
// parse is shown as stored: the raw source beats an error message.
export async function formatScript(code: string): Promise<string> {
  const [prettier, babel, estree] = await Promise.all([
    import("prettier/standalone"),
    import("prettier/plugins/babel"),
    import("prettier/plugins/estree"),
  ]);
  try {
    return await prettier.format(code, {
      parser: "babel",
      plugins: [babel, estree],
    });
  } catch {
    return code;
  }
}
