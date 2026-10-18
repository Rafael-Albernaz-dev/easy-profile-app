# Architecture Rules, Guardrails & Quality Gates (`EasyProfileApp` v2)

Este documento consolida as regras de engenharia, os guardrails de proteção do áudio em produção, o isolamento hermético de testes e os portões de qualidade proporcionais do `EasyProfileApp`, revisados após a auditoria técnica cruzada.

---

## 1. Guardrails de Segurança do Áudio em Produção

1. **Preservação dos Guards Atuais até a Slice `EP-006`:**
   - Os serviços `ka1333-input-guard.service` e `easyeffects-output-guard.service` permanecem ativos e habilitados durante as slices `EP-001` a `EP-005`.
   - No cutover (`EP-006`), o instalador salva o estado anterior (`is-active` / `is-enabled`) de cada serviço para que o rollback restaure exatamente a condição prévia (sem `enable --now` cego).
2. **Exceção Controlada para a Slice `EP-002` (Spikes Acompanhados):**
   - Por padrão, ferramentas de desenvolvimento operam em modo Read-Only (`--observe-only`).
   - Na Slice `EP-002`, são permitidos testes pontuais e acompanhados pelo usuário para validar volume/mute/rota reais, desde que: (a) não desabilitem os guards ativos, (b) façam snapshot do estado anterior antes do comando, e (c) restaurem o estado imediatamente após a medição.
3. **Imutabilidade dos Presets Originais do EasyEffects:**
   - Os arquivos em `~/.config/easyeffects/output/` e `~/.config/easyeffects/input/` jamais são modificados pelo `EasyProfileApp`.
4. **Execução Segura de Subprocessos:**
   - Proibido usar `bash -c` ou `sh -c`. Todo comando externo nos adaptadores reais usa `exec.CommandContext` com argumentos explícitos e timeout.

---

## 2. Isolamento Hermético de Testes Automatizados (Anti-Gaming)

> [!IMPORTANT]
> **Lição da Auditoria:** `dbus-run-session` isola apenas o barramento D-Bus; ele **NÃO** isola o socket do PipeWire, o hardware ALSA nem o banco `dconf` do usuário!

Portanto, para garantir que `go test ./...` e `./scripts/quality-gate.sh` tenham **Zero Efeito Colateral no Host**:
1. **Fakes por Construção:** Nos testes automatizados, o daemon e o controlador recebem exclusivamente adaptadores em memória (`FakePipeWire`, `FakeAlsa`, `FakeEasyEffects`) via injeção de dependência.
2. **Bloqueio por Variável de Ambiente e `PATH` em Testes:** A suíte de testes define `EASYPROFILE_TEST_MODE=1` (que faz os adaptadores reais de comando recusarem execução com panic/erro caso sejam instanciados por engano em um teste) e utiliza diretórios temporários (`t.TempDir()`) + `GSETTINGS_BACKEND=memory` quando aplicável.
3. **Zero Testes Cosméticos:** Só escrevemos testes unitários e de regressão para invariantes reais de áudio/estado (ex: Snapshot de 3 booleans do Pânico, override manual durante o Pânico, arbitragem de preset da sessão vs autoload, sufixo dinâmico do KA-1333, fragmentos truncados do `pw-dump`).

---

## 3. Metas de Performance e Recursos (Medir Antes de Limitar)

Em vez de impor um `CPUQuota=2%` no `systemd` que pode causar *throttling* de CPU justamente durante o spawn de subprocessos de troca de rota ou mute:
1. **Medição Reproduzível:** Medimos nas slices `EP-004` e `EP-006` a latência real de IPC D-Bus, o tempo de parse do dump do PipeWire e o RSS real do processo (`ps`/`/proc/<pid>/status`) + subprocessos.
2. **Metas Operacionais:**
   - Consumo de CPU em repouso (sem eventos no grafo): desprezível (`~0%` numa janela de 60s).
   - Resposta do comando D-Bus (daemon em memória): `< 20 ms`.
   - Limites de proteção no `systemd` (`MemoryMax`, `TasksMax`) serão calibrados na `EP-006` com margem segura (ex: `MemoryMax=64M`, sem estrangular o pico de troca de rota com `CPUQuota=2%`).

---

## 4. Quality Gates Proporcionais por Slice (`./scripts/quality-gate.sh`)

O script `./scripts/quality-gate.sh` executa verificações proporcionais aos componentes já existentes na slice atual (sem falhar cobrando arquivos de slices futuras e sem usar falsos positivos como `node --check` para provar semântica do GNOME Shell):

- **Gate 1 (Formatação & Estática):** `gofmt -s -l .` + `go vet ./...` + compilação dos binários existentes.
- **Gate 2 (Segurança & Higiene Git):** Verifica ausência de `bash -c`/`sh -c`, ausência de comandos destrutivos nos testes, e garante que `AGENTS.md`, `GEMINI.md` e `.agents/` estão fora do `git ls-files`.
- **Gate 3 (Integridade dos Presets & Extensão GJS):** Verifica que os hashes SHA-256 dos 3 presets reais em `~/.config/easyeffects/` permanecem idênticos aos originais e, a partir da slice GJS, valida o schema GSettings (`glib-compile-schemas --strict --dry-run`) e os arquivos da extensão.
- **Gate 4 (Unitários & Regressão `-race`):** `go test -race ./...` com `EASYPROFILE_TEST_MODE=1`.
- **Gate 5 (Integração D-Bus Isolada — a partir da EP-004):** `dbus-run-session` exercitando CLI ↔ Daemon com Fakes injetados.
- **Gate 6 (Diagnóstico Read-Only & Benchmarks):** Executa os benchmarks existentes e confere que os serviços `ka1333-input-guard` e `easyeffects-output-guard` continuam ativos no host (enquanto estivermos nas slices `EP-001` a `EP-005`).
