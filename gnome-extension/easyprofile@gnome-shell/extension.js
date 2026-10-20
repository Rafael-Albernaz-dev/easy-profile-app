import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Meta from 'gi://Meta';
import Shell from 'gi://Shell';
import St from 'gi://St';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import * as PopupMenu from 'resource:///org/gnome/shell/ui/popupMenu.js';
import * as Slider from 'resource:///org/gnome/shell/ui/slider.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

import {DaemonProxy} from './dbus.js';

export default class EasyProfileExtension extends Extension {
    enable() {
        this._enabled = true;
        this._generation = (this._generation ?? 0) + 1;
        this._cancellable = new Gio.Cancellable();
        this._pendingLevels = new Map();
        this._levelInFlight = new Set();
        this._levelGeneration = 0;
        this._retrySources = new Set();
        this._barriers = [];
        this._barrierBusy = false;
        this._settings = this.getSettings();
        this._keybindingKeys = [];
        this._status = null;
        this._targetChannel = 'out';
        this._updatingSliders = false;

        // 1. Build Top Bar Indicator & Menu
        this._buildIndicator();

        // 2. Connect to D-Bus Service
        this._initDBus();

        // 3. Connect to NumLock via Clutter Keymap
        this._initKeymap();
    }

    disable() {
        this._enabled = false;
        this._generation++;
        this._cancellable?.cancel();
        this._cancellable = null;
        this._pendingLevels?.clear();
        this._levelInFlight?.clear();
        this._barriers = [];
        this._barrierBusy = false;
        for (const source of this._retrySources ?? []) GLib.Source.remove(source);
        this._retrySources?.clear();
        if (this._ownerSignalId && this._proxy) {
            this._proxy.disconnect(this._ownerSignalId);
            this._ownerSignalId = 0;
        }
        this._unregisterKeybindings();

        if (this._keymap && this._numLockSignalId) {
            this._keymap.disconnect(this._numLockSignalId);
            this._numLockSignalId = null;
            this._keymap = null;
        }

        if (this._proxySignalId && this._proxy) {
            this._proxy.disconnectSignal(this._proxySignalId);
            this._proxySignalId = null;
        }
        this._proxy = null;

        if (this._indicator) {
            this._indicator.destroy();
            this._indicator = null;
        }

        this._settings = null;
        this._status = null;
        for (const field of ['_box', '_icon', '_label', '_muteBadge', '_micMuteBadge', '_listenBadge', '_menu',
            '_routeBox', '_btnAKG', '_btnSP166', '_volItem', '_volIcon', '_volSlider',
            '_btnMuteOut', '_micItem', '_micIcon', '_micSlider', '_btnMuteMic', '_micListenItem', '_shareItem',
            '_presetsSubMenu', '_warningBanner', '_rawStreamMenu']) this[field] = null;
        this._keymap = null;
    }

    _buildIndicator() {
        this._indicator = new PanelMenu.Button(0.0, 'EasyProfile Indicator', false);
        this._box = new St.BoxLayout({style_class: 'panel-status-indicators-box'});

        this._icon = new St.Icon({
            icon_name: 'audio-headphones-symbolic',
            style_class: 'system-status-icon',
        });
        this._label = new St.Label({
            text: 'EasyProfile desconectado',
            y_align: Clutter.ActorAlign.CENTER,
            style_class: 'system-status-label',
        });

        this._box.add_child(this._icon);
        this._muteBadge = new St.Icon({icon_name: 'audio-volume-muted-symbolic', style_class: 'system-status-icon', visible: false});
        this._box.add_child(this._muteBadge);
        this._micMuteBadge = new St.Icon({
            icon_name: 'microphone-sensitivity-muted-symbolic',
            style_class: 'system-status-icon',
            style: 'color: #e01b24;',
            visible: false,
        });
        this._box.add_child(this._micMuteBadge);
        this._listenBadge = new St.Icon({
            icon_name: 'audio-headset-symbolic',
            style_class: 'system-status-icon',
            style: 'color: #3584e4;',
            visible: false,
        });
        this._box.add_child(this._listenBadge);
        this._box.add_child(this._label);
        this._indicator.add_child(this._box);

        Main.panel.addToStatusArea('easyprofile-indicator', this._indicator);

        // Build Popup Menu
        this._menu = this._indicator.menu;

        // Header: Title
        const titleItem = new PopupMenu.PopupMenuItem('EasyProfile Audio', {reactive: false});
        titleItem.label.add_style_class_name('popup-subtitle-menu-item');
        this._menu.addMenuItem(titleItem);
        this._menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());

        // Routes Switcher
        this._routeBox = new St.BoxLayout({style_class: 'popup-menu-item', x_expand: true});
        this._btnAKG = new St.Button({
            label: '🎧 AKG K52',
            style_class: 'button',
            can_focus: true,
            x_expand: true,
        });
        this._btnAKG.connect('clicked', () => {
            this._targetChannel = 'out';
            if (this._status?.active_destination?.id !== 'akg') {
                this._callDBus('SelectDestination', ['akg']);
            }
            this._updateLabel();
        });

        this._btnSP166 = new St.Button({
            label: '🔊 SP166 (Caixas)',
            style_class: 'button',
            can_focus: true,
            x_expand: true,
        });
        this._btnSP166.connect('clicked', () => {
            this._targetChannel = 'out';
            if (this._status?.active_destination?.id !== 'sp166') {
                this._callDBus('SelectDestination', ['sp166']);
            }
            this._updateLabel();
        });

        this._routeBox.add_child(this._btnAKG);
        this._routeBox.add_child(this._btnSP166);
        this._menu.box.add_child(this._routeBox);

        this._menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());

        // Output Volume Slider & Mute Toggle
        this._volItem = new PopupMenu.PopupBaseMenuItem({activate: false});
        this._volIcon = new St.Icon({icon_name: 'audio-volume-high-symbolic', icon_size: 16});
        this._volSlider = new Slider.Slider(0.95);
        this._volSlider.x_expand = true;
        this._volSlider.connect('notify::value', () => {
            if (this._updatingSliders) return;
            const pct = Math.round(this._volSlider.value * 100);
            this._queueLevel('out', pct, true);
        });
        this._btnMuteOut = new St.Button({
            style_class: 'icon-button',
            child: new St.Icon({icon_name: 'audio-volume-muted-symbolic', icon_size: 16}),
        });
        this._btnMuteOut.connect('clicked', () => this._callDBus('ToggleOutputMute', []));

        this._volItem.add_child(this._volIcon);
        this._volItem.add_child(this._volSlider);
        this._volItem.add_child(this._btnMuteOut);
        this._menu.addMenuItem(this._volItem);

        // Mic Level Slider & Dual-Mute Toggle
        this._micItem = new PopupMenu.PopupBaseMenuItem({activate: false});
        this._micIcon = new St.Icon({icon_name: 'audio-input-microphone-symbolic', icon_size: 16});
        this._micSlider = new Slider.Slider(1.0);
        this._micSlider.x_expand = true;
        this._micSlider.connect('notify::value', () => {
            if (this._updatingSliders) return;
            const pct = Math.round(this._micSlider.value * 100);
            this._queueLevel('mic', pct, true);
        });
        this._btnMuteMic = new St.Button({
            style_class: 'icon-button',
            child: new St.Icon({icon_name: 'microphone-sensitivity-muted-symbolic', icon_size: 16}),
        });
        this._btnMuteMic.connect('clicked', () => this._callDBus('ToggleMicMute', []));

        this._micItem.add_child(this._micIcon);
        this._micItem.add_child(this._micSlider);
        this._micItem.add_child(this._btnMuteMic);
        this._menu.addMenuItem(this._micItem);

        // Mic Monitoring / Listen Button
        this._micListenItem = new PopupMenu.PopupSwitchMenuItem('Escutar microfone (com filtros) [7]', false);
        this._micListenItem.connect('toggled', (_item, state) => {
            if (this._updatingListen) return;
            this._callDBus('ToggleMicMonitoring', [], err => {
                if (!err && state && (this._status?.mic_privacy_muted || this._status?.mic_virtual_muted || this._status?.mic_physical_muted)) {
                    this._callDBus('ToggleMicMute', []);
                }
            });
        });
        this._menu.addMenuItem(this._micListenItem);

        this._shareItem = new PopupMenu.PopupSwitchMenuItem('Compartilhar toda a saída na chamada', false);
        this._shareItem.connect('toggled', (_item, enabled) => {
            if (this._updatingShare) return;
            this._setOutputSharing(enabled);
        });
        this._menu.addMenuItem(this._shareItem);

        this._menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());

        // Presets Submenu
        this._presetsSubMenu = new PopupMenu.PopupSubMenuMenuItem('Presets de Equalização');
        this._menu.addMenuItem(this._presetsSubMenu);

        // Reset Base Button
        const resetItem = new PopupMenu.PopupMenuItem('Restaurar Padrão Diário [*]');
        resetItem.connect('activate', () => this._callDBus('ResetDefaults', []));
        this._menu.addMenuItem(resetItem);

        // Restart Daemon Button
        this._menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());
        this._restartItem = new PopupMenu.PopupMenuItem('🔄 Reiniciar EasyProfile');
        this._restartItem.connect('activate', () => this._restartDaemon());
        this._menu.addMenuItem(this._restartItem);

        // Raw Mic Stream Warning Banner
        this._warningBanner = new PopupMenu.PopupMenuItem('⚠️ Aplicativo acessando mic físico!', {reactive: false});
        this._warningBanner.visible = false;
        this._menu.addMenuItem(this._warningBanner);
        this._rawStreamMenu = new PopupMenu.PopupSubMenuMenuItem('Mover aplicativo para o microfone processado');
        this._rawStreamMenu.visible = false;
        this._menu.addMenuItem(this._rawStreamMenu);
    }

    _restartDaemon() {
        this._invalidateLevels();
        const generation = this._generation;
        this._showOSD('Iniciando EasyProfile...', 'system-run-symbolic');
        try {
            const proc = Gio.Subprocess.new(
                ['systemctl', '--user', 'restart', 'easyprofiled.service'],
                Gio.SubprocessFlags.NONE
            );
            proc.wait_async(this._cancellable, (process, result) => {
                try {
                    process.wait_finish(result);
                    if (this._enabled && generation === this._generation) {
                        if (!process.get_successful()) {
                            this._showOSD('Falha ao reiniciar EasyProfile', 'dialog-error-symbolic');
                        } else {
                            this._showOSD('EasyProfile reiniciado!', 'emblem-ok-symbolic');
                            this._initDBus();
                        }
                    }
                } catch (error) {
                    if (this._enabled && generation === this._generation)
                        this._showOSD(error.message, 'dialog-error-symbolic');
                }
            });
        } catch (error) { this._showOSD(error.message, 'dialog-error-symbolic'); }
    }

    _setConnected(connected) {
        if (!this._enabled) return;
        if (this._volItem) this._volItem.reactive = connected;
        if (this._micItem) this._micItem.reactive = connected;
        if (this._btnAKG) this._btnAKG.reactive = connected;
        if (this._btnSP166) this._btnSP166.reactive = connected;
        if (this._presetsSubMenu) this._presetsSubMenu.reactive = connected;
        if (this._shareItem) this._shareItem.reactive = connected;
        if (this._restartItem?.label?.set_text) {
            this._restartItem.label.set_text(connected ? '🔄 Reiniciar EasyProfile' : '⚡ Forçar Ativação do EasyProfile');
        }
        if (!connected) {
            this._invalidateLevels();
            this._barriers = [];
            this._status = null;
            if (this._label?.set_text) this._label.set_text('EasyProfile desconectado (clique para ativar)');
            if (this._icon?.set_icon_name) this._icon.set_icon_name('network-offline-symbolic');
            if (this._muteBadge) this._muteBadge.visible = false;
            if (this._micMuteBadge) this._micMuteBadge.visible = false;
            if (this._listenBadge) this._listenBadge.visible = false;
        }
    }

    _initDBus() {
        const generation = this._generation;
        this._setConnected(false);
        try {
            this._proxy = new DaemonProxy(
                Gio.DBus.session, 'org.easyprofile.Daemon', '/org/easyprofile/Daemon',
                (proxy, error) => {
                    if (!this._enabled || generation !== this._generation) return;
                    if (error) { this._setConnected(false); console.error(error); return; }
                    this._proxySignalId = proxy.connectSignal('StateChanged', (_p, _sender, [json]) => {
                        if (this._enabled && generation === this._generation) this._onStateChanged(json);
                    });
                    this._ownerSignalId = proxy.connect('notify::g-name-owner', () => {
                        if (!this._enabled || generation !== this._generation) return;
                        this._setConnected(Boolean(proxy.g_name_owner));
                        if (proxy.g_name_owner) this._refreshStatus();
                    });
                    this._setConnected(Boolean(proxy.g_name_owner));
                    if (proxy.g_name_owner) this._refreshStatus();
                }, this._cancellable
            );
        } catch (error) { console.error(error); }
    }

    _refreshStatus(done = null, retryCount = 0) {
        if (!this._enabled || !this._proxy?.g_name_owner) { done?.(null); return; }
        const generation = this._generation;
        const owner = this._proxy.g_name_owner;
        this._proxy.GetStatusRemote((result, error) => {
            if (!this._enabled || generation !== this._generation) return;
            if (owner !== this._proxy?.g_name_owner) { done?.(null); return; }
            if (error || !result) {
                if (retryCount < 3 && this._proxy?.g_name_owner) {
                    const source = GLib.timeout_add(GLib.PRIORITY_DEFAULT, 200, () => {
                        this._retrySources.delete(source);
                        if (this._enabled && generation === this._generation && this._proxy?.g_name_owner) {
                            this._refreshStatus(done, retryCount + 1);
                        }
                        return GLib.SOURCE_REMOVE;
                    });
                    this._retrySources.add(source);
                    return;
                }
                this._setConnected(false);
                done?.(null);
                return;
            }
            this._onStateChanged(result[0]);
            this._setConnected(true);
            done?.(this._status);
        }, this._cancellable);
    }

    _onStateChanged(statusJson) {
        try {
            const s = JSON.parse(statusJson);
            const prev = this._status;
            if (this._status?.active_destination?.id !== s.active_destination.id)
                this._invalidateLevels();
            this._status = s;

            // Trigger OSD on profile/destination or preset change
            if (prev) {
                const destChanged = prev.active_destination?.id !== s.active_destination?.id;
                const presetChanged = prev.active_preset && s.active_preset && prev.active_preset !== s.active_preset;
                if (destChanged) {
                    const isAKG = s.active_destination?.id === 'akg';
                    const icon = isAKG ? 'audio-headphones-symbolic' : 'audio-speakers-symbolic';
                    const name = s.active_destination?.display_name || (isAKG ? 'Fone AKG K52' : 'Caixas SP166');
                    const presetInfo = s.active_preset ? `\nPreset: ${s.active_preset}` : '';
                    this._showOSD(`${name}${presetInfo}`, icon, 2000);
                } else if (presetChanged) {
                    const isAKG = s.active_destination?.id === 'akg';
                    const icon = isAKG ? 'audio-headphones-symbolic' : 'audio-speakers-symbolic';
                    const presets = s.available_presets || [];
                    const idx = presets.indexOf(s.active_preset);
                    const counter = idx >= 0 ? ` (${idx + 1}/${presets.length})` : '';
                    this._showOSD(`🎛️ Preset${counter}\n${s.active_preset}`, icon, 2000);
                }
            }

            // 1. Update Top Bar Icon & Label
            const isAKG = s.active_destination.id === 'akg';
            let iconName = isAKG ? 'audio-headphones-symbolic' : 'audio-speakers-symbolic';
            if (this._muteBadge) this._muteBadge.visible = Boolean(s.observed_output_mute);
            if (this._micMuteBadge) this._micMuteBadge.visible = Boolean(s.mic_privacy_muted || s.mic_virtual_muted || s.mic_physical_muted);
            if (this._listenBadge) this._listenBadge.visible = Boolean(s.mic_monitoring);
            if (this._icon?.set_icon_name) this._icon.set_icon_name(iconName);

            this._updateLabel();

            // 2. Update Route Buttons Styling
            if (isAKG) {
                this._btnAKG.add_style_pseudo_class('active');
                this._btnSP166.remove_style_pseudo_class('active');
            } else {
                this._btnSP166.add_style_pseudo_class('active');
                this._btnAKG.remove_style_pseudo_class('active');
            }

            // 3. Update Sliders (without re-triggering remote calls)
            this._updatingSliders = true;
            this._volSlider.value = this._displayLevel('out') / 100.0;
            this._micSlider.value = this._displayLevel('mic') / 100.0;
            this._updatingSliders = false;

            // 4. Update Mute button styles
            if (s.output_muted) {
                this._btnMuteOut.add_style_pseudo_class('active');
            } else {
                this._btnMuteOut.remove_style_pseudo_class('active');
            }

            if (s.mic_privacy_muted) {
                this._btnMuteMic.add_style_pseudo_class('active');
            } else {
                this._btnMuteMic.remove_style_pseudo_class('active');
            }

            // Update Mic Monitoring Switch
            if (this._micListenItem) {
                this._updatingListen = true;
                this._micListenItem.setToggleState(Boolean(s.mic_monitoring));
                this._updatingListen = false;
            }
            if (this._shareItem) {
                this._updatingShare = true;
                this._shareItem.setToggleState(Boolean(s.output_sharing || s.output_sharing_restore_pending));
                this._shareItem.label.set_text(s.output_sharing_restore_pending
                    ? 'Compartilhamento: concluir restauração da entrada'
                    : 'Compartilhar toda a saída na chamada');
                this._updatingShare = false;
            }

            // 5. Update Presets Submenu
            this._presetsSubMenu.menu.removeAll();
            for (const presetName of s.available_presets || []) {
                const isActive = presetName === s.active_preset;
                const item = new PopupMenu.PopupMenuItem(
                    `${isActive ? '✓ ' : '   '}${presetName}`
                );
                item.connect('activate', () => {
                    this._callDBus('SelectPreset', [presetName]);
                });
                this._presetsSubMenu.menu.addMenuItem(item);
            }

            // 6. Update Warning Banner
            const hasRawStreams = s.raw_mic_streams && s.raw_mic_streams.length > 0;
            this._warningBanner.visible = Boolean(hasRawStreams || s.last_error);
            this._rawStreamMenu.visible = Boolean(hasRawStreams);
            this._rawStreamMenu.menu.removeAll();
            for (const application of s.raw_mic_streams || []) {
                const confirm = new PopupMenu.PopupSubMenuMenuItem(application);
                const action = new PopupMenu.PopupMenuItem(`Confirmar migração de ${application}`);
                action.connect('activate', () => this._callDBus('MigrateRawMicStream', [application]));
                confirm.menu.addMenuItem(action);
                this._rawStreamMenu.menu.addMenuItem(confirm);
            }
            if (hasRawStreams) {
                this._warningBanner.label.set_text(`⚠️ Mic físico acessado: ${s.raw_mic_streams.join(', ')}`);
            } else if (s.last_error) {
                this._warningBanner.label.set_text(`⚠️ Proteção incompleta: ${s.last_error}`);
            }
        } catch (error) { console.error(error); }
    }

    _setOutputSharing(enabled) {
        const generation = this._generation;
        this._shareItem.reactive = false;
        this._callDBus('SetOutputSharing', [enabled], () => {
            if (!this._enabled || generation !== this._generation || !this._shareItem) return;
            this._shareItem.reactive = Boolean(this._proxy?.g_name_owner);
            this._refreshStatus();
        });
    }

    _updateLabel() {
        if (!this._status) return;
        const isAKG = this._status.active_destination?.id === 'akg';
        const destShort = isAKG ? 'AKG' : 'SP166';
        const numLockOn = this._keymap ? Boolean(this._keymap.get_num_lock_state()) : false;
        const modeMarker = numLockOn ? '⚡' : '';
        const observeMarker = this._status.observe_only ? ' (observação)' : '';
        const shareMarker = this._status.output_sharing ? ' • COMPARTILHANDO' :
            (this._status.output_sharing_restore_pending ? ' • RESTAURAR ENTRADA' : '');
        if (numLockOn && this._targetChannel === 'mic') {
            this._label.set_text(`${modeMarker}MIC ${this._displayLevel('mic')}%${observeMarker}${shareMarker}`);
        } else {
            this._label.set_text(`${modeMarker}${destShort} ${this._displayLevel('out')}%${observeMarker}${shareMarker}`);
        }
    }

    _selectPresetByIndex(index) {
        const presets = this._status?.available_presets;
        if (presets && presets.length > index) {
            const target = presets[index];
            if (target && target !== this._status?.active_preset) {
                this._callDBus('SelectPreset', [target]);
            }
        }
    }

    _selectBasePreset() {
        const presets = this._status?.available_presets;
        if (!presets || presets.length === 0) return;
        const basePreset = presets[0];
        if (this._status?.active_preset !== basePreset) {
            this._callDBus('SelectPreset', [basePreset]);
        } else {
            const isAKG = this._status?.active_destination?.id === 'akg';
            const icon = isAKG ? 'audio-headphones-symbolic' : 'audio-speakers-symbolic';
            this._showOSD(`🎛️ Preset Base (já ativo)\n${basePreset}`, icon, 2000);
        }
    }

    _cyclePreset(delta) {
        const presets = this._status?.available_presets;
        if (!presets || presets.length === 0) return;
        const isAKG = this._status?.active_destination?.id === 'akg';
        const icon = isAKG ? 'audio-headphones-symbolic' : 'audio-speakers-symbolic';
        if (presets.length === 1) {
            this._showOSD(`🎛️ Preset Único (1/1)\n${presets[0]}`, icon, 2000);
            return;
        }
        const current = this._status?.active_preset;
        let idx = presets.indexOf(current);
        if (idx === -1) idx = 0;
        const nextIdx = (idx + delta + presets.length) % presets.length;
        const target = presets[nextIdx];
        if (target) {
            this._callDBus('SelectPreset', [target]);
        }
    }

    _callDBus(method, args, done = null) {
        if (['SelectDestination', 'ToggleDestination', 'ResetDefaults', 'SelectPreset'].includes(method)) {
            this._invalidateLevels();
            this._barriers.push({method, args, done});
            this._drainBarriers();
            return;
        }
        if (method === 'TogglePanic') this._invalidateLevels();
        this._invokeDBus(method, args, error => {
            done?.(error);
            if (!error) this._refreshStatus();
        });
    }

    _invokeDBus(method, args, done) {
        if (!this._enabled || !this._proxy?.g_name_owner) {
            if (this._enabled) this._showOSD('Daemon EasyProfile desconectado', 'dialog-error-symbolic');
            done?.(new Error('daemon disconnected'));
            return;
        }
        const generation = this._generation;
        const owner = this._proxy.g_name_owner;
        try {
            if (typeof this._proxy[`${method}Remote`] !== 'function') {
                throw new Error(`Método ${method} indisponível`);
            }
            this._proxy[`${method}Remote`](...args, (_res, error) => {
                if (!this._enabled || generation !== this._generation || owner !== this._proxy?.g_name_owner) {
                    done?.(new Error('obsolete D-Bus call'));
                    return;
                }
                if (error) {
                    const msg = error.message.replace(/^GDBus\.Error:[a-zA-Z0-9_.]+: /, '');
                    this._showOSD(`Falha: ${msg}`, 'dialog-error-symbolic');
                    done?.(error);
                    return;
                }
                done?.(null);
            }, this._cancellable);
        } catch (error) {
            this._showOSD(error.message, 'dialog-error-symbolic');
            done?.(error);
        }
    }

    _invalidateLevels() {
        this._levelGeneration++;
        this._pendingLevels?.clear();
    }

    _drainBarriers() {
        if (!this._enabled || this._barrierBusy || this._levelInFlight.size || !this._barriers.length) return;
        const generation = this._generation;
        const {method, args, done} = this._barriers.shift();
        this._barrierBusy = true;
        this._invokeDBus(method, args, error => {
            if (!this._enabled || generation !== this._generation) return;
            // Keep input gated until the route/reset readback has arrived.
            this._refreshStatus(() => {
                if (!this._enabled || generation !== this._generation) return;
                this._barrierBusy = false;
                done?.(error);
                this._drainBarriers();
            });
        });
    }

    _levelKey(channel) { return `${channel}-${this._status?.active_destination?.id}`; }

    _displayLevel(channel) {
        return this._pendingLevels.get(this._levelKey(channel))?.value ??
            (channel === 'mic' ? this._status.mic_level : this._status.output_volume);
    }

    // One in-flight level request per channel; sliders replace the pending target,
    // repeated keys accumulate locally instead of flooding the hardware queue.
    _queueLevel(channel, value, absolute) {
        if (!this._enabled || !this._status || this._barrierBusy || this._barriers.length) return;
        const key = this._levelKey(channel);
        const destination = this._status.active_destination.id;
        const generation = this._generation;
        const levelGeneration = this._levelGeneration;
        let pending = this._pendingLevels.get(key);
        if (!pending) {
            pending = {value: channel === 'mic' ? (this._status?.mic_level ?? 100) : (this._status?.output_volume ?? 95), busy: false, dirty: false};
            this._pendingLevels.set(key, pending);
        }
        pending.value = Math.max(0, Math.min(100, absolute ? value : pending.value + value));
        pending.dirty = true;

        this._updateLabel();

        const flush = () => {
            if (!this._enabled || generation !== this._generation || levelGeneration !== this._levelGeneration ||
                this._pendingLevels.get(key) !== pending || pending.busy || !pending.dirty) return;
            pending.dirty = false;
            pending.busy = true;
            this._levelInFlight.add(pending);
            const method = channel === 'mic' ? 'SetMicVolumeAbsolute' : 'SetVolumeForDestination';
            const args = channel === 'mic' ? [pending.value] : [destination, pending.value];
            this._callDBus(method, args, error => {
                pending.busy = false;
                this._levelInFlight.delete(pending);
                if (!this._enabled || generation !== this._generation) return;
                if (this._pendingLevels.get(key) === pending) {
                    if (!error && pending.dirty) flush();
                    else this._pendingLevels.delete(key);
                    if (error) this._updateLabel();
                }
                this._drainBarriers();
            });
        };
        flush();
    }

    _initKeymap() {
        try {
            const seat = Clutter.get_default_backend().get_default_seat();
            this._keymap = seat ? seat.get_keymap() : null;
        } catch (_e) {
            this._keymap = null;
        }

        if (this._keymap) {
            this._numLockSignalId = this._keymap.connect('notify::num-lock-state', () => {
                this._syncNumLockState();
            });
            this._syncNumLockState();
        }
    }

    _syncNumLockState() {
        if (!this._keymap) return;
        const numLockOn = Boolean(this._keymap.get_num_lock_state());

        if (numLockOn) {
            this._registerKeybindings();
        } else {
            this._targetChannel = 'out';
            this._unregisterKeybindings();
        }

        this._updateLabel();
    }

    _registerKeybindings() {
        if (this._keybindingKeys.length > 0) return;

        const mode = Shell.ActionMode.NORMAL | Shell.ActionMode.OVERVIEW;
        const noRepeat = Meta.KeyBindingFlags.IGNORE_AUTOREPEAT;
        const allowRepeat = Meta.KeyBindingFlags.NONE;

        const bindings = [
            // 1) TECLA "+": aumenta o volume em 1% (saída ou microfone)
            {name: 'shortcut-volume-up', flags: allowRepeat, handler: () => {
                this._queueLevel(this._targetChannel, 1, false);
            }},

            // 2) TECLA "-": diminui o volume em 1% (saída ou microfone)
            {name: 'shortcut-volume-down', flags: allowRepeat, handler: () => {
                this._queueLevel(this._targetChannel, -1, false);
            }},

            // 3) TECLA "1": seleciona o AKG K52 (se já ativo, feedback OSD; restaura foco para saída)
            {name: 'shortcut-dest-akg', flags: noRepeat, handler: () => {
                this._targetChannel = 'out';
                if (this._status?.active_destination?.id !== 'akg') {
                    this._callDBus('SelectDestination', ['akg']);
                } else {
                    const preset = this._status?.active_preset ? `\nPreset: ${this._status.active_preset}` : '';
                    this._showOSD(`Fone AKG K52 (ativo)${preset}`, 'audio-headphones-symbolic', 2000);
                }
                this._updateLabel();
            }},

            // 4) TECLA "2": seleciona o SP166 (se já ativo, feedback OSD; restaura foco para saída)
            {name: 'shortcut-dest-sp166', flags: noRepeat, handler: () => {
                this._targetChannel = 'out';
                if (this._status?.active_destination?.id !== 'sp166') {
                    this._callDBus('SelectDestination', ['sp166']);
                } else {
                    const preset = this._status?.active_preset ? `\nPreset: ${this._status.active_preset}` : '';
                    this._showOSD(`Caixas SP166 (ativo)${preset}`, 'audio-speakers-symbolic', 2000);
                }
                this._updateLabel();
            }},

            // 8) TECLA "3": seleciona o microfone para ajuste com "+" ou "-"
            {name: 'shortcut-select-mic', flags: noRepeat, handler: () => {
                this._targetChannel = (this._targetChannel === 'mic') ? 'out' : 'mic';
                const isMic = this._targetChannel === 'mic';
                this._showOSD(
                    isMic ? '🎙️ Foco: Ganho do Microfone (+/-)' : '🔊 Foco: Volume da Saída (+/-)',
                    isMic ? 'audio-input-microphone-symbolic' : 'audio-volume-high-symbolic',
                    1500
                );
                this._updateLabel();
            }},

            // 10) TECLA "4": Preset Base (Padrão Diário Aprovado)
            {name: 'shortcut-preset-1', flags: noRepeat, handler: () => {
                this._selectBasePreset();
            }},

            // 11) TECLA "5": Preset Anterior (Circular)
            {name: 'shortcut-preset-2', flags: noRepeat, handler: () => {
                this._cyclePreset(-1);
            }},

            // 12) TECLA "6": Próximo Preset (Circular)
            {name: 'shortcut-preset-3', flags: noRepeat, handler: () => {
                this._cyclePreset(1);
            }},

            // 5) TECLA "0": muta/desmuta microfone (Dual-Mute)
            {name: 'shortcut-mute-mic', flags: noRepeat, handler: () => {
                this._callDBus('ToggleMicMute', []);
            }},

            // 6) TECLA ",": muta/desmuta saída
            {name: 'shortcut-mute-output', flags: noRepeat, handler: () => {
                this._callDBus('ToggleOutputMute', []);
            }},

            // 7) TECLA "ENTER": muta/desmuta saída e entrada (Pânico / Mute All)
            {name: 'shortcut-mute-panic', flags: noRepeat, handler: () => {
                this._callDBus('TogglePanic', [], err => {
                    if (!err) {
                        const panic = this._status?.panic_active;
                        this._showOSD(
                            panic ? '🚨 PÂNICO ATIVADO (Tudo Mutado)' : '✓ Pânico Desativado (Áudio Restaurado)',
                            panic ? 'dialog-warning-symbolic' : 'audio-volume-high-symbolic'
                        );
                    }
                });
            }},

            // 13) TECLA "/": ativa DynamicEqualizer (placeholder)
            {name: 'shortcut-dynamic-eq', flags: noRepeat, handler: () => {
                console.log('EasyProfile: DynamicEqualizer ativado (placeholder)');
            }},

            // '*' restores daily levels/preset while preserving mic mutes; no restart.
            {name: 'shortcut-reset-app', flags: noRepeat, handler: () => {
                this._callDBus('ResetDefaults', [], err => {
                    if (!err) {
                        this._showOSD('✓ Padrão Diário Restaurado\nAKG K52 (95%) • Mic (100%)', 'audio-headphones-symbolic', 2000);
                    }
                });
            }},

            // 14) TECLA "7": escutar microfone com os filtros (Toggle)
            {name: 'shortcut-noop-7', flags: noRepeat, handler: () => {
                const nextActive = !this._status?.mic_monitoring;
                this._callDBus('ToggleMicMonitoring', [], err => {
                    if (!err) {
                        if (nextActive && (this._status?.mic_privacy_muted || this._status?.mic_virtual_muted || this._status?.mic_physical_muted)) {
                            this._callDBus('ToggleMicMute', []);
                            this._showOSD('🎧 Escuta Ativada (Mic Desmutado)', 'audio-headset-symbolic');
                        } else {
                            this._showOSD(
                                nextActive ? '🎧 Escuta do Microfone: Ativada' : 'Escuta do Microfone: Desativada',
                                'audio-headset-symbolic'
                            );
                        }
                    }
                });
            }},
            {name: 'shortcut-noop-8', flags: noRepeat, handler: () => {
                this._callDBus('ToggleOutputSharing', []);
            }},
            {name: 'shortcut-noop-9', flags: noRepeat, handler: () => {}},
        ];


        for (const b of bindings) {
            const action = Main.wm.addKeybinding(b.name, this._settings, b.flags, mode, () => {
                if (this._enabled && this._keymap?.get_num_lock_state()) b.handler();
            });
            if (action === Meta.KeyBindingAction.NONE)
                console.error(`EasyProfile: atalho indisponível ${b.name}`);
            else this._keybindingKeys.push(b.name);
        }
    }

    _unregisterKeybindings() {
        for (const name of this._keybindingKeys) {
            Main.wm.removeKeybinding(name);
        }
        this._keybindingKeys = [];
    }

    _showOSD(message, iconName, durationMs = 1500) {
        try {
            const icon = Gio.ThemedIcon.new(iconName || 'audio-volume-high-symbolic');
            Main.osdWindowManager.show(
                -1,
                icon,
                message,
                null,
                null
            );
            if (durationMs > 1500) {
                const repeatCount = Math.floor((durationMs - 500) / 1000);
                for (let i = 1; i <= repeatCount; i++) {
                    const source = GLib.timeout_add(GLib.PRIORITY_DEFAULT, i * 1000, () => {
                        this._retrySources?.delete(source);
                        if (this._enabled) {
                            try {
                                Main.osdWindowManager.show(-1, icon, message, null, null);
                            } catch (_e) {}
                        }
                        return GLib.SOURCE_REMOVE;
                    });
                    this._retrySources?.add(source);
                }
            }
        } catch (_e) {}
    }

}
