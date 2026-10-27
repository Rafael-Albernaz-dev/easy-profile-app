# Push-to-talk no teclado numérico

Com NumLock no modo áudio, segure `/` por três segundos para entrar no modo.
O microfone começa mutado nos dois alvos (virtual pós-DSP e físico). Segurar `0`
libera esses dois mutes; soltar volta a mutar. Um novo toque em `/` sai do modo,
mantendo o microfone mutado. Fora do modo, `0` continua alternando dual-mute.
O antigo placeholder de DynamicEqualizer foi removido desse atalho.

Menu e `easyprofilectl ptt on|off` também controlam o modo. Status JSON publica
`push_to_talk`, `talk_pressed` (somente com ambos os readbacks desmutados) e
`talk_epoch`. A Top Bar distingue `PTT MUTADO` e `PTT FALANDO`.
O mute do próprio WhatsApp/navegador continua sendo controlado pelo aplicativo:
PTT do sistema não remove o mute dentro da chamada.

## Proteções e limites concretos

- O modo não é restaurado. Um marcador durável de privacidade registra que
  um hold não pode virar mic aberto no reinício: ajustes de nível e outras gravações durante
  PTT persistem ambos os mutes como true. No mesmo login, reinício confirma
  dual-mute antes de limpar esse marcador e começa fora do modo. Shutdown normal encerra um hold e muta o microfone.
- Entrada/saída do modo mutam, sem restaurar automaticamente um mic aberto.
- Pânico, Reset e seleção de destino encerram o modo. Pânico conserva o snapshot
  independente de saída/mic virtual/mic físico; um hold não é retomado.
- Compartilhamento e escuta do mic são incompatíveis com PTT; ligar PTT encerra
  compartilhamento, e escuta já ligada exige desligamento explícito.
- Tokens por modo e sequência crescente rejeitam comandos antigos, inclusive um
  press que chegue depois de release. Mute manual revoga o token atual.
- Enquanto `0` está segurado, a extensão renova uma concessão a cada 750 ms.
  A renovação não executa comandos de áudio nem persiste sessão. Após dois segundos
  sem renovação aceita, o daemon solicita dual-mute prioritário. Subprocessos,
  fila e readbacks continuam sujeitos aos deadlines; não é um prazo acústico
  absoluto de dois segundos. Falhas de mute aparecem no status.
- Uma tecla segurada tem limite de 60 s; solte e pressione novamente para continuar.
- Desligar NumLock, desabilitar a extensão ou perder o daemon encerra o hold local.
  O daemon possui o prazo independente para falha/morte da UI.

GNOME 46 fornece callbacks de press nos atalhos. Para acompanhar release com uma
janela Wayland focada, a extensão usa um grab temporário do Shell apenas enquanto
`/` ou `0` estiverem segurados. **Outras teclas não chegam ao aplicativo durante
esse hold.** Falha em obter o teclado impede abertura do mic. Esse comportamento
precisa de homologação física; callbacks em memória não provam eventos Wayland.
Fontes primárias: [Mutter 46 keybindings](https://gitlab.gnome.org/GNOME/mutter/-/blob/46.0/src/core/keybindings.c)
e [Shell 46 modal grabs](https://gitlab.gnome.org/GNOME/gnome-shell/-/blob/46.0/js/ui/main.js).

## Verificação

Go com fakes cobre hold/release, sequências antigas, renovação sem comandos,
expiração, falha de readback, observe-only, pânico, Reset, destino e mute manual.
O harness executa os handlers reais da extensão com timers/grabs/proxy em memória.
CLI, daemon e proxy GJS real são exercitados em D-Bus privado com sentinelas para
comandos de áudio. Tudo usa `EASYPROFILE_TEST_MODE=1`.

Para aceite humano: volume baixo, uma chamada de teste; `/` curto não ativa;
`/` por 3s ativa e muta; segure/solte `0` e confirme recepção/silêncio remoto;
`/` sai mantendo mute; repita NumLock off e pânico durante um hold. Confira
mutes independentes, ausência de modo restaurado e hashes dos presets.

Morte abrupta do daemon não executa mute: o prazo de dois segundos depende de
ele estar vivo. O marcador garante recuperação no reinício, não silenciamento
durante a ausência do processo. O mute dentro da chamada permanece independente.
