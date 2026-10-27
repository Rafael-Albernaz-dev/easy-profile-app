# Output Boost and keypad 8/9

## User contract

- With NumLock audio mode enabled, **8 toggles whole-output sharing**; **9 toggles output Boost**. Both ignore key autorepeat.
- Boost unlocks an output ceiling of **150%** without raising the current level. `+`/`−`, the output slider and CLI can then request up to 150%. Microphone stays capped at 100%.
- Disabling clamps output levels above 100% to 100%; lower levels remain unchanged. Reset restores the daily 95% output and 100% microphone while retaining microphone mutes and disabling Boost.
- Boost and levels above 100% are transient. Restart/login starts without Boost. Presets and destination changes retain Boost during the current daemon run and use the selected destination's remembered level.
- The menu exposes Boost and the panel displays `BOOST`; status JSON includes `output_boost` and `output_max_percent`. CLI: `boost on|off|toggle`, then e.g. `volume 105`.

## Engineering and audio

The existing domain uses an output-only constructor permitting 150%. Physical endpoints and microphone retain the normal 0–100% constructor. Logical output percentage is separate from PipeWire volume: requests above 100% keep the virtual sink at 100% and apply the excess gain inside EasyEffects. Saved session levels are capped at 100%, so restart cannot restore an elevated level inadvertently.

A reserved **`limiter#99`** is appended after the selected preset in runtime GSettings. No preset JSON is written. Its input gain is `60 * log10(max(100, percent)/100)` dB, matching the cubic UI scale: 150% is about +10.57 dB. It uses LSP's stereo limiter, Herm Thin, 0 dB threshold, no automatic gain compensation, 5 ms lookahead, 1 ms attack, 20 ms fixed release (the longest valid value in the installed [0.25, 20.0] ms range; ALR remains disabled), stereo link 100% and no oversampling. Other plugin settings and the preset's ordered chain are preserved. All parameters, ranges and enums are pre-validated against the installed schema before writing limiter parameters, and a partial failure attempts bounded rollback, reports cleanup errors, restores previous non-gain values and keeps extra gain at zero. The daemon leaves physical output muted on failure. This is compensating recovery, not an atomic dconf transaction.

EasyEffects 7.1.6 applies the limiter's input gain before running the limiter: [native implementation](https://github.com/wwmm/easyeffects/blob/v7.1.6/src/limiter.cpp). Its per-instance settings paths derive from the plugin ID: [instance construction](https://github.com/wwmm/easyeffects/blob/v7.1.6/src/effects_base.cpp). Dependency verified on this host: `/usr/lib/lv2/lsp-plugins.lv2/sc_limiter_stereo.ttl`.

This increases quieter material and controls peaks; already loud material may mainly receive limiting rather than a full +10.57 dB increase. Limiting can alter dynamics. No claim of transparent sound, measured true-peak protection or zero additional latency is made. Lookahead and the limiter add DSP cost while Boost is active; exact CPU and end-to-end latency require physical measurement. Boost off removes the extra stage.

## Guards and lifecycle

Only the serialized worker changes the mode. Mode changes drain/discard pending UI levels, mute the physical output while rebuilding the chain and restore its previous mute state after successful readback. Limiter parameters are configured before positive gain. Failed configuration/readback keeps physical output muted and publishes an error. Unknown preset chains and presets using the reserved instance are refused. Disabling zeros extra gain before removing only the reserved tail.

Preset loading reapplies the overlay before declaring the preset confirmed. Route changes synchronize gain with that destination's logical level before reloading the preset. Panic retains its three independent mute booleans; microphone mute state is untouched by Boost. Graceful daemon shutdown removes the overlay; startup preset application removes a stale tail after an interrupted process. Reserved limiter settings may remain in dconf while inactive; they are not preset files.

The normal healthy observation path adds no Boost settings read when Boost is off. With Boost on, active preset validation additionally reads the reserved limiter settings. There is no separate DSP process or new watcher.

## Verification and limits

Hermetic race tests cover unlock without level change, 150% cap, 100% microphone/physical limits, no persistence of extra gain, preset/route changes, panic, Reset, limiter readback failures and observe-only refusal. Extension tests execute actual binding handlers and cover NumLock, autorepeat, pending-volume barriers and UI caps. Private D-Bus tests exercise CLI, daemon and the exact GJS proxy, including 150% then return to 100%.

The official gate still stops at its legacy guard-active expectation after the completed cutover. That check is not bypassed; actual service/preset integrity is recorded separately. Measured in isolation: parser 3.497 ms/op; fake CLI p95 5.257 ms (maximum 7.036 ms) over 100 samples; fake idle CPU 0% of one core over 60 s; RSS after CLI 9,716 KiB. These satisfy the corresponding captured/fake budgets and do not measure real Boost DSP cost. Local raw logs are in ignored `docs/evidence/`.

## Human acceptance after loading the new daemon and extension

1. Start with low output. NumLock on: press **8**, confirm sharing indicator and remote reception in one active call; press again to restore microphone. Notifications/participant voices are included. Disable microphone listening first.
2. At 100% output, press **9**: level stays 100%, `BOOST` appears. Use `+` to 101–150%; further increments stop at 150%. Microphone stays capped at 100%.
3. Toggle Boost off above 100%: level becomes 100%, extra gain is removed and the preset remains selected. Repeat while below 100%: level stays unchanged.
4. With Boost active, change presets and AKG/SP166 destinations; confirm each remembered volume, physical inactive connector mute and no unexpected increase on the other destination.
5. Verify microphone mute, panic restore and Reset. Reset disables Boost and sharing while preserving microphone mutes. Restart/login starts without Boost.
6. Listen for clipping, pumping, clicks and delay on quiet/loud material. Compare EasyEffects CPU with Boost off/on under the same load. Preset SHA-256 must remain unchanged.

No live gain, route, mute or DSP setting was changed during automated validation. Physical audio quality and browser-call acceptance remain pending.

## Remediação atual: evidência e hipóteses

O release=50 era inválido no schema instalado; isso foi confirmado por introspecção.
O teto anterior de 112 estava explícito no domínio/UI. O contrato atual pede 150.
O mute ao desligar ainda não tem causa física isolada: a implementação não
aguardava reconstrução do grafo, nem reaplicava explicitamente o nível normal.
Agora OFF confirma ganho zero antes de remover o tail, aguarda readback e caminho
DSP conectado, reaplica saída virtual <=100 e restaura somente o mute físico lido
antes da transação bem-sucedida. Timeout/cancelamento mantém proteção e erro.

A confirmação de mudança de cadeia exige caminho conectado do sink virtual por
cada plugin esperado até um sink físico, com links ativos/pausados. Presença de
arquivo LV2 ou GSettings sozinhos não basta. Isso prova topologia, não qualidade
acústica ou ausência de clipping. Carregamento de preset continua assíncrono,
com confirmação da identidade/cadeia pedida e deadline/cancelamento.

O limitador temporário usa oversampling None e release fixo de 20 ms para reduzir
custo e modulação rápida. São mitigação a avaliar, sem causa comprovada para
stuttering. Presets calibrados, inclusive seus próprios limitadores, não mudam.
Acima de 100%, passos alteram o ganho do limitador sem reescrever sink em 1.0 nem
refazer fsync de snapshot durável idêntico. Renovação de PTT também não cria
comandos de áudio. Consulte o [gate operacional](../performance/OPERATIONAL_QUALITY_GATE.md).

Estado desta remediação: testes herméticos disponíveis; instalação e homologação
real devem constar do relatório da sessão. Históricos acima não certificam a
versão atual. Não executar testes automatizados em áudio real.

## Saída ociosa e instalação de 20/10/2026

Quando não há reprodução, o EasyEffects pode remover nós e links de plugins.
Somente um sink explicitamente `idle`/`suspended`, sem entrada ativa, permite
adiar a prova do caminho nativo. Estado desconhecido ou reprodução ativa exige
cadeia conectada. `output_dsp_idle=true` com caminho ausente nunca publica
`output_boost_confirmed=true`: a extensão mostra **BOOST AGUARDANDO ÁUDIO**.
O início da reprodução dispara observação e confirmação pelo grafo existente.

Uma primeira instalação foi revertida porque o check nativo tratava saída ociosa
como falha de inicialização. A versão corrigida foi instalada com backup em
`/tmp/easyprofile-install-20261020`; o binário em disco e em execução corresponde
a SHA-256 `e8301571aa05266606caa93f73f62eea555735b82e105289e33b246f3a301d70`.
A extensão versão2 foi confirmada carregada após relogin. GNOME46 não recarrega
outro módulo ESM da mesma extensão na sessão; disable/enable não certifica versão
nova. Nenhum preset foi escrito. O daemon ficou saudável após os nós do
EasyEffects aparecerem; no login ocorreram duas tentativas anteriores por ausência
dos endpoints, registradas no journal, sem declaração de startup sem falhas.

A comparação real acompanhada usa o mesmo binário, preset, áudio e nível60%
com Boost off/on. Mede o custo do estágio adicional em regime; não mede rajadas
de teclas, 150% de ganho, troca de saída, PTT ou recepção no WhatsApp.
Esses cenários exigem provas próprias. Consulte o relatório operacional para
métricas, limitações dos contadores ERR e estado do gate.
