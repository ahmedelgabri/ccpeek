

# CCPeek

Explora el historial de tus agentes de codificación. Una aplicación web local que indexa
**Claude Code, Pi, Codex CLI y OpenCode** (además de
**Cursor**, experimental: consulta la matriz de capacidades a continuación) en una
base de datos centrada en sesiones: conversaciones, planes, tareas pendientes, tareas, instantáneas de shell,
historial de archivos, caché de portapapeles, memorias y comandos, con uso real
de tokens y costo estimado, más una interfaz de consulta orientada a agentes
(`ccpeek query`, `/api/v1`, `ccpeek mcp`) y actualizaciones en vivo. Todo
permanece en esta máquina.

<img width="1728" height="1085" alt="Screenshot 2026-07-28 at 14 38 11" src="https://github.com/user-attachments/assets/734e5f7d-207a-4b60-8df3-b32a49f11349" />

## Instalación

### Homebrew

```sh
brew install ahmedelgabri/tap/ccpeek
```

### Nix Flakes

```sh
# Run directly
nix run github:ahmedelgabri/ccpeek

# Or install into your profile
nix profile install github:ahmedelgabri/ccpeek
```

### Compilar desde el código fuente (producto completo)

Requiere Go 1.25+, Node.js y pnpm (para compilar la interfaz web):

```sh
# Clone and build
git clone https://github.com/ahmedelgabri/ccpeek.git
cd ccpeek
pnpm install
just build
# Binary is at cmd/ccpeek/ccpeek
```

El producto completo se compila con la etiqueta de compilación `withui`, la cual
exige la presencia de la UI incrustada en tiempo de compilación; `just build` (y
cualquier ruta de lanzamiento) no puede generar un binario sin interfaz.

### `go install` (variante solo API)

Un `go build ./...` o `go install` sencillo no puede ejecutar la compilación del SPA, por lo que
produce deliberadamente la **variante solo API**: `/api/v1`,
`ccpeek query` y `ccpeek mcp` funcionan normalmente, el servidor registra una
advertencia al iniciarse, y `/` explica qué falta en lugar de
mostrar una página en blanco. Úsalo para configuraciones sin interfaz/solo agentes; usa cualquier
otro método de instalación para la interfaz web.

### Binarios precompilados

Descarga desde [GitHub Releases](https://github.com/ahmedelgabri/ccpeek/releases).
Los archivos incluyen complementos de shell y páginas de manual.

## Uso

```sh
# Index detected agent roots and start the web UI
ccpeek

# Open browser automatically
ccpeek --open

# Use a different port
ccpeek --port 8080

# Skip re-indexing on subsequent runs
ccpeek --skip-index

# Index only (no server)
ccpeek --index-only
```

El servidor lee los datos de cada agente desde su raíz predeterminada (para Claude Code,
`~/.claude`), escribe un índice en `$XDG_DATA_HOME/ccpeek/ccpeek2.db` y
sirve la interfaz web en `http://localhost:3000`.

El puerto se vincula inmediatamente; la indexación se ejecuta en segundo plano con el progreso en
stderr, y la interfaz se llena en vivo a medida que llegan los datos (`/api/v1/ready` responde
200 una vez completada la primera pasada). Después de la primera compilación completa,
los archivos sin cambios se omiten mediante una verificación de tamaño+mtime sin volver a
leerlos, por lo que los inicios posteriores siguen siendo rápidos incluso con historiales de múltiples GB.

### Banderas

| Bandera         | Predeterminado                       | Descripción                                                                                                           |
| --------------- | ------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| `-p`, `--port`  | `3000`                               | Puerto del servidor                                                                                                   |
| `--claude-dir`  | `~/.claude`                          | Directorio de origen (datos de Claude)                                                                                |
| `--data-file`   | `$XDG_DATA_HOME/ccpeek/ccpeek2.db`   | Ubicación de la base de datos: el índice se deriva de esta ruta. Una base de datos heredada en esta ubicación se importa una sola vez; consulta la [FAQ](#faq) |
| `--skip-index`  | `false`                              | Omitir la indexación, servir datos existentes                                                                         |
| `--index-only`  | `false`                              | Indexar y salir                                                                                                       |
| `-o`, `--open`  | `false`                              | Abrir el navegador después de iniciar                                                                                 |
| `-w`, `--watch` | `false`                              | Reindexar mientras se sirve, ante cambios en el sistema de archivos                                                   |
| `--rebuild`     | `false`                              | Forzar reconstrucción completa (eliminar todos los datos y reindexar)                                                 |
| `--prune`       | `false`                              | Eliminar datos de archivos de origen que ya no existan                                                                |
| `--skip-scan`   | `false`                              | Omitir el análisis de secretos después de la indexación                                                               |
| `-q`, `--quiet` | `false`                              | Suprimir salida informativa                                                                                           |

### Complementos de shell

```sh
# Bash
ccpeek completion bash >/etc/bash_completion.d/ccpeek

# Zsh
ccpeek completion zsh >"${fpath[1]}/_ccpeek"

# Fish
ccpeek completion fish >~/.config/fish/completions/ccpeek.fish
```

Las instalaciones de Homebrew y Nix incluyen complementos y páginas de manual automáticamente.

### Subcomandos

#### `ccpeek scan`

Analiza los datos indexados en busca de secretos filtrados, claves API, tokens y contraseñas. Utiliza
las reglas de detección de gitleaks (más de 150 patrones). Los resultados se almacenan en la base de datos
y son visibles en la interfaz web en `/scan`. El índice se actualiza de forma incremental
antes del análisis para cubrir el historial recién escrito (`--no-index` lo omite).

```sh
ccpeek scan
```

#### `ccpeek export commands`

Exporta comandos de shell extraídos de sesiones de agentes indexadas en formato de historial de shell.

```sh
# Plain (one command per line)
ccpeek export commands

# Append to zsh history
ccpeek export commands --format zsh >> ~/.zsh_history && fc -R

# Append to bash history
ccpeek export commands --format bash >> ~/.bash_history && history -r

# Append to fish history
ccpeek export commands --format fish >> ~/.local/share/fish/fish_history

# Filter by workspace path or date range
ccpeek export commands --project myapp --from 2025-01-01 --to 2025-06-01
```

## Qué indexa

- **Sesiones** - Conversaciones de todos los agentes compatibles, con tokens y costo
- **Artefactos** - Planes, tareas pendientes, tareas, instantáneas de shell, caché de portapapeles, datos de
  uso, memorias e historial de archivos, vinculados a sus sesiones
- **Comandos** - Comandos de shell extraídos de las sesiones
- **Uso** - Agregaciones de tokens/costo por día, modelo, espacio de trabajo y agente
- **Análisis de secretos** - Detecta secretos filtrados en todos los datos indexados

### Matriz de capacidades de agentes

| Agente      | Estado           | Mensajes | Uso/costo | Llamadas a herramientas | Notas                                                                                                                                 |
| ----------- | ---------------- | -------- | ---------- | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| Claude Code | compatible       | ✓        | ✓          | ✓                       | Sesiones, todos los artefactos sidecar, historial de prompts, análisis incremental de la cola                                         |
| Pi          | compatible       | ✓        | ✓          | ✓                       | Formato de sesión documentado; bifurcaciones/ramas; costos reportados                                                                 |
| Codex CLI   | compatible       | ✓        | ✓          | ✓                       | Conteos acumulativos de tokens recuperados por turno; el razonamiento es un subconjunto de la salida                                  |
| OpenCode    | compatible       | ✓        | ✓          | ✓                       | Costos reportados preferidos; razonamiento aditivo integrado en la salida facturable                                                  |
| Cursor      | **experimental** | ✓        | ✓          | —                       | Esquema derivado de fixtures, aún no validado contra un `store.db` real; sin extracción de herramientas. Se esperan lagunas hasta la validación con datos reales |

## Interfaz orientada a agentes

Toda lectura está disponible de tres formas: `ccpeek query` (JSON en la
línea de comandos), `/api/v1` (HTTP) y `ccpeek mcp` (MCP sobre stdio),
generadas desde una definición compartida, por lo que las interfaces nunca se desincronizan.

```sh
# Query as JSON (no server needed; exit 3 = valid query, no matches)
ccpeek query sessions --agent codex --since 2026-07-01
ccpeek query session claude-code <session-id>
ccpeek query transcript pi <session-id> --limit 50
ccpeek query usage --group model
ccpeek query search "rate limiting"

# Serve the UI + API (--watch adds fsnotify re-indexing + SSE live updates)
ccpeek --watch

# MCP server over stdio (register: claude mcp add ccpeek -- ccpeek mcp)
ccpeek mcp

# Secret-scan every agent's history (not just Claude's)
ccpeek scan

# Shell-history export, ingest diagnostics, agent cheatsheet
ccpeek export commands --format zsh
ccpeek ingest --latest
ccpeek docs --agents

# Install the ccpeek skill into ~/.claude/skills (or --dir for another harness)
ccpeek skill install
```

Las raíces de datos del agente se resuelven como: configuración explícita >
sobreescritura de variables de entorno del propio agente (`CLAUDE_CONFIG_DIR`, `PI_CODING_AGENT_DIR`, `CODEX_HOME`,
`OPENCODE_DATA_DIR`; Cursor no tiene, por lo que ccpeek respeta
`CCPEEK_CURSOR_DIR`) > valores predeterminados de la plataforma.

## Desarrollo

Usa [Nix](https://nixos.org/) con `nix develop` para un entorno de desarrollo completo,
o instala Go 1.25+, Node.js, pnpm y [just](https://github.com/casey/just) manualmente.

```sh
pnpm install

# Run dev server (builds the UI, opens browser)
just dev

# Vite dev server with HMR against a running ccpeek
just ui-dev

# Run all tests
just test

# Run unit tests only
just test-unit

# Run e2e tests only
just test-e2e

# Lint
just lint

# Format
just format
```

## Preguntas frecuentes

### Usé ccpeek antes, ¿qué pasa con mis datos antiguos (v1)?

CCPeek es una reescritura centrada en sesiones y multiagente de la
herramienta original de un solo agente, y se actualiza automáticamente en la primera ejecución: sin
pasos, sin banderas. Ingere las raíces de agentes detectadas en un nuevo índice
(`ccpeek2.db`, escrito junto al antiguo `ccpeek.db`) e importa los
datos exclusivos de v1 que no pueden rederivarse de los archivos de origen: sesiones
cuyas fuentes fueron eliminadas, y tus banderas de ignorar análisis. La base de datos v1
se abre en solo lectura y nunca se modifica, por lo que revertir es simplemente ejecutar
la versión antigua. Consulta [docs/v2-plan.md](docs/v2-plan.md) para el
diseño completo.

### ¿Mis marcadores y URLs antiguos siguen funcionando?

Sí. Cada URL heredada (`/projects/…`, `/plans/`, `/commands/`, marcadores de
sesión, el montaje de vista previa `/v2/`) redirige permanentemente a su
equivalente centrado en sesiones.

### ¿Dónde se almacena la base de datos y qué es `--data-file`?

El índice reside en `$XDG_DATA_HOME/ccpeek/ccpeek2.db`. `--data-file`
nombra la ruta de la base de datos heredada; el
índice actual se deriva de ella como archivo hermano (`ccpeek.db` →
`ccpeek2.db`, `x.db` → `x.v2.db`). Un archivo heredado en esa ruta
se importa una sola vez (solo lectura); no apuntes `--data-file` a un
índice actual.

### ¿Por qué se ignora `--watch-interval`?

Se acepta únicamente por compatibilidad con versiones anteriores. ccpeek reindexa
ante eventos del sistema de archivos (usa `--watch`) en lugar de por un temporizador.
