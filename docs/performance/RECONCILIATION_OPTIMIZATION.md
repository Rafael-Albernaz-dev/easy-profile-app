# Otimização da reconciliação — 19/10/2026

Esta fatia reduz o trabalho de observação do controlador. Não implementa Boost,
não modifica presets e não reinicia o serviço real para medir desempenho.
As alterações anteriores de compartilhamento de áudio foram preservadas.

## Mudanças e invariantes

- `ReadObservation` reúne os checks de grafo, streams crus, rota, escuta e
  compartilhamento em um único snapshot novo de `pw-dump`. Não há cache desse
  snapshot entre observações nem depois de mutações.
- Os três readbacks de mute continuam pelo caminho `wpctl` existente. Erro de
  observação não vira `false` conhecido nem confirmação de proteção.
- Uma reconciliação sem reparação usa a inspeção inicial validada. Após qualquer
  reparação de rota/DSP/mutes/preset/compartilhamento, uma nova inspeção continua
  obrigatória. A ordem e os tetos acústicos não foram ampliados.
- A validação do preset continua conferindo arquivo, bypass e ordem de plugins;
  apenas as leituras duplicadas foram removidas.
- Observação idêntica do microfone não regrava a sessão após uma persistência
  bem-sucedida. A primeira gravação e tentativas após erro continuam ocorrendo,
  inclusive quando os valores em memória já são iguais.
- O observador `dconf` sinaliza uma vez por caminho, sem sinalizar de novo pelas
  linhas de valor/separação. Nenhuma chave foi filtrada. Formato inesperado causa
  erro e reconexão, em vez de ignorar mudanças silenciosamente.
- A janela existente de 100 ms consome o evento acumulado antes da inspeção;
  eventos recebidos durante a reconciliação permanecem pendentes para outra
  passagem. A janela é limitada e respeita cancelamento.
- Snapshot desconhecido mantém um transporte de compartilhamento vivo visível
  como potencialmente ativo, com restauração pendente. Pânico e dual-mute seguem
  independentes e cobertos pelas regressões anteriores.

## Comparativo realizado sem controlar áudio

Diagnóstico opt-in:

```bash
go run ./cmd/easyprofile-perfprobe --live-read-only --samples 5
```

O diagnóstico só chama métodos de leitura. Não instancia daemon nem chama
`Reconcile`, pois este pode reparar hardware. Recusa execução quando
`EASYPROFILE_TEST_MODE` está presente. Os testes automatizados continuam usando
fakes, com essa variável definida.

Cinco pares de leituras foram comparados no host. Os estados observados foram
iguais em cada par. O caminho anterior simula duas inspeções independentes mais
a validação adicional do preset; o otimizado usa uma inspeção consolidada.

| Métrica das leituras equivalentes | Anterior | Otimizado |
|---|---:|---:|
| Mediana de duração | 889,80 ms | 218,66 ms |
| Mediana de CPU, processo e filhos encerrados | 598,51 ms | 123,99 ms |
| Subprocessos de leitura em regime, contagem pelo código | 31 | 11 |

Redução observada: 75,43% na mediana de duração e 79,28% no tempo de CPU das
leituras. A contagem de subprocessos considera identidades aquecidas e
compartilhamento desligado; resolução inicial pode exigir leitura adicional.
Esses resultados não representam o consumo contínuo do daemon otimizado.
A métrica anterior de 8,8% de um núcleo era uma amostra de uso real da versão
em execução, não uma carga fixa de benchmark.

Amostras passivas separadas de 12 s dos observadores PipeWire e dconf não tiveram
eventos. Por isso não se atribui todo o consumo anterior a um ciclo de eventos
permanente. A redução de trabalho redundante foi comprovada independentemente
dessa hipótese. Ainda será necessário comparar a CPU do serviço após carregar a
nova versão, sob uma carga equivalente de eventos e comandos.

## Verificação e limites

Regressões incluem: uma inspeção por reconciliação saudável; inspeção adicional
após reparação; falha de preset sem confirmação falsa; observação nova em cada
chamada; falha de snapshot com compartilhamento vivo; no-op de persistência sem
perder retry; agrupamento de eventos e cancelamento.

Os gates oficiais continuam bloqueando na etapa 3: os guards legados estão
inativos após o cutover, embora o script exija `active`. Os presets de saída
Padrão foram renomeados externamente e o baseline ainda contém os caminhos
anteriores; os conteúdos originais são conferidos pelos SHA-256, sem renomeá-los
nem reescrever o baseline. Não se declara aprovação integral dos seis gates.

O teste hermético com race detector, a auditoria AST, vet e staticcheck passaram.
A integração D-Bus privada passou sem comandos de áudio no host; os seis testes
do instalador com fakes e o empacotamento da extensão também passaram.
O orçamento do parser passou em repetição isolada (4,125 ms/op); a primeira
execução concorrente marcou 5,894 ms/op, acima dos 5 ms previstos.
O benchmark fake de CLI permaneceu acima do orçamento de 15 ms no p95:
21,73 ms inicialmente e 17,57 ms na repetição. Nesta última amostra de 60 s,
a CPU fake foi 0% e o RSS final após comandos foi 9.692 KiB. Isso registra uma
pendência de latência, não uma aprovação integral de performance.

## Organização do repositório

Documentação foi agrupada por finalidade, configuração de exemplo movida para
`configs/` e programas Go auxiliares para `cmd/`. Scripts de instalação, testes,
gates e referências foram ajustados. Domínio, orquestração e adaptadores mantêm
suas responsabilidades existentes. Presets e fixtures continuam versionados.
Evidências brutas e `gschemas.compiled` deixam de ser rastreados, mas permanecem
no disco; a arquitetura solicitada permanece local e ignorada em `docs/`.

## Homologação após carregar a versão otimizada

1. Confirmar entrada/saída/preset e mutes antes e depois do login.
2. Exercitar volume, presets, troca de saída, dual-mute, pânico e Reset; verificar
   preservação dos mutes independentes e retorno do estado observado.
3. Testar escuta e compartilhamento da fatia anterior.
4. Comparar CPU/RSS do serviço completo em 60 s, com reprodução e uso equivalentes;
   separar o custo da extensão GNOME e o processamento DSP do EasyEffects.
5. Verificar reconciliação de mudança externa, reconexão de dispositivo e ausência
   de novos erros de áudio. Não confundir testes fake com homologação física.

## Auditoria adicional: reconhecimento e defaults em chamadas

Um subagente fez esta análise em paralelo, somente em leitura. No momento da
inspeção, `default.audio.source` e `default.configured.audio.source` apontavam para
`easyeffects_source`; os dois defaults de saída apontavam para `easyeffects_sink`.
As descrições atuais são “Easy Effects Source” e “Easy Effects Sink”.

O app já estabelece ambos os padrões ao vincular o DSP. Há uma lacuna anterior
à otimização: `GraphState`/`DSPBound` verificam continuamente apenas os defaults de
saída; a escrita do padrão de entrada não confirma os dois defaults por readback.
Uma mudança externa apenas da entrada pode passar sem reparação. Isso ficou
auditado, não corrigido nesta fatia. A correção posterior está
descrita em [defaults processados de entrada/saída](../features/SYSTEM_DEFAULT_AUDIO.md).

Nomes como “EasyProfile — Microfone processado” e “EasyProfile — Saída processada”
facilitariam a seleção humana, preservando os `node.name` usados pelo controlador.
A propagação dos nomes ao navegador e sua restauração após recriação dos nós
ainda precisam ser verificadas. Não é preciso adicionar DSP apenas para nomear.

O padrão do sistema não substitui necessariamente a escolha do navegador ou da
chamada. O Chrome tem seu próprio [microfone padrão para sites](https://support.google.com/chrome/answer/2693767?hl=en),
e o Meet permite [selecionar entrada e saída](https://support.google.com/meet/answer/10409699?hl=en-NZ).
A especificação define `label` para reconhecimento humano, não como indicação
semântica que obrigue o site a escolher um dispositivo; os IDs também têm regras
por origem: [Media Capture and Streams](https://www.w3.org/TR/mediacapture-streams/#dom-mediadeviceinfo-label).
A saída específica pode ser escolhida pela aplicação via
[`setSinkId`](https://developer.chrome.com/blog/audiocontext-setsinkid).

A Meta confirma [chamadas no WhatsApp Web](https://about.fb.com/news/2026/07/whatsapp-web-calling-new-features/),
sem prometer seleção automática de dispositivos virtuais. É necessária validação
separada no WhatsApp e Meet, com uma chamada já aberta e após novo login. As
escolhas explícitas das aplicações são respeitadas pela política local do
WirePlumber 0.4.17; não há promessa de migrar todas as chamadas automaticamente.

## Estado preparado para o próximo login

O binário otimizado foi instalado atomicamente em `~/.local/bin/easyprofilectl`,
com SHA-256 `658d5e2a297da24ff151c89e6398eaaf908e2282fa2a30b2f3971b232c3c5d89`.
Backup em `/tmp/easyprofile-optimization-install-wwdnd96v`. O serviço não foi
reiniciado: PID 805952 permaneceu igual e ainda executa a versão anterior.
A nova versão será carregada no próximo início do serviço, esperado após novo login.

Antes e depois: `easyprofiled.service` ativo e ambos os guards legados inativos.
Os três SHA-256 originais dos presets foram localizados e conferidos sem alterações.
Manifesto local: `docs/evidence/optimization-2026-10-19-integrity.json`.
