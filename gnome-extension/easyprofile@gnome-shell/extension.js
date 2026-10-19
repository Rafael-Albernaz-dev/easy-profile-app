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
        this._settings = this.getSettings();
        this._keybindingKeys = [];
        this._status = null;
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
        this._btnAKG.connect('clicked', () => this._callDBus('SelectDestination', ['akg']));

        this._btnSP166 = new St.Button({
            label: '🔊 SP166 (Caixas)',
            style_class: 'button',
            can_focus: true,
            x_expand: true,
        });
        this._btnSP166.connect('clicked', () => this._callDBus('SelectDestination', ['sp166']));

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

        this._menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());

        // Presets Submenu
        this._presetsSubMenu = new PopupMenu.PopupSubMenuMenuItem('Presets de Equalização');
        this._menu.addMenuItem(this._presetsSubMenu);

        // Reset Base Button
        const resetItem = new PopupMenu.PopupMenuItem('Restaurar Padrão Diário [5]');
        resetItem.connect('activate', () => this._callDBus('ResetDefaults', []));
        this._menu.addMenuItem(resetItem);

        // Raw Mic Stream Warning Banner
        this._warningBanner = new PopupMenu.PopupMenuItem('⚠️ Aplicativo acessando mic físico!', {reactive: false});
        this._warningBanner.visible = false;
        this._menu.addMenuItem(this._warningBanner);
        this._rawStreamMenu = new PopupMenu.PopupSubMenuMenuItem('Mover aplicativo para o microfone processado');
        this._rawStreamMenu.visible = false;
        this._menu.addMenuItem(this._rawStreamMenu);
    }

    _setConnected(connected) {
        if (!this._enabled) return;
        this._menu.box.reactive = connected;
        this._volItem.reactive = connected;
        this._micItem.reactive = connected;
        this._btnAKG.reactive = connected;
        this._btnSP166.reactive = connected;
        if (!connected) {
            this._pendingLevels?.clear();
            this._status = null;
            this._label.set_text('EasyProfile desconectado');
            this._icon.set_icon_name('network-offline-symbolic');
            this._muteBadge.visible = false;
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
                    GLib.timeout_add(GLib.PRIORITY_DEFAULT, 200, () => {
                        if (this._enabled && generation === this._generation && this._proxy?.g_name_owner) {
                            this._refreshStatus(done, retryCount + 1);
                        }
                        return GLib.SOURCE_REMOVE;
                    });
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
            this._status = s;

            // 1. Update Top Bar Icon & Label
            const isAKG = s.active_destination.id === 'akg';
            let iconName = isAKG ? 'audio-headphones-symbolic' : 'audio-speakers-symbolic';
            this._muteBadge.visible = Boolean(s.observed_output_mute);
            this._icon.set_icon_name(iconName);

            const destShort = isAKG ? 'AKG' : 'SP166';
            const numLockOn = this._keymap ? Boolean(this._keymap.get_num_lock_state()) : false;
            const modeMarker = numLockOn ? '⚡' : '';
            this._label.set_text(`${modeMarker}${destShort} ${s.output_volume}%${s.observe_only ? ' (observação)' : ''}`);

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
            this._volSlider.value = s.output_volume / 100.0;
            this._micSlider.value = s.mic_level / 100.0;
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

    _callDBus(method, args, done = null) {
        if (!this._enabled || !this._proxy?.g_name_owner) {
            this._showOSD('Daemon EasyProfile desconectado', 'dialog-error-symbolic');
            done?.();
            return;
        }
        const generation = this._generation;
        const owner = this._proxy.g_name_owner;
        this._proxy[`${method}Remote`](...args, (_res, error) => {
            if (!this._enabled || generation !== this._generation) return;
            if (owner !== this._proxy?.g_name_owner) { done?.(); return; }
            if (error) {
                const msg = error.message.replace(/^GDBus\.Error:[a-zA-Z0-9_.]+: /, '');
                this._showOSD(`Falha: ${msg}`, 'dialog-error-symbolic');
                done?.();
                return;
            }
            this._refreshStatus(s => {
                if (!s) { done?.(); return; }
                if (method.includes('Volume')) {
                    const mic = method.includes('Mic');
                    this._showOSDGauge(mic ? 'Nível Microfone' : 'Volume Saída', mic ? s.mic_level : s.output_volume,
                        mic ? 'audio-input-microphone-symbolic' : 'audio-volume-high-symbolic');
                } else {
                    let text = `${s.active_destination.display_name} · ${s.active_preset}`;
                    if (method === 'TogglePanic') text = s.last_error ? 'Proteção incompleta' : (s.panic_active ? 'Pânico confirmado' : 'Snapshot restaurado');
                    if (method === 'ToggleMicMute') text = s.mic_privacy_muted ? 'Dual-Mute confirmado' : 'Microfone: mute alterado';
                    if (method === 'ToggleOutputMute') text = s.observed_output_mute ? 'Saída mutada' : 'Saída desmutada';
                    if (method === 'ResetDefaults') text = 'Padrão diário restaurado';
                    if (method === 'MigrateRawMicStream') text = 'Migração ao microfone processado confirmada';
                    this._showOSD(text, 'audio-headphones-symbolic');
                }
                done?.();
            });
        }, this._cancellable);
    }

    // One in-flight level request per channel; sliders replace the pending target,
    // repeated keys accumulate locally instead of flooding the hardware queue.
    _queueLevel(channel, value, absolute) {
        let pending = this._pendingLevels.get(channel);
        if (!pending) {
            pending = {value: channel === 'mic' ? (this._status?.mic_level ?? 100) : (this._status?.output_volume ?? 95), busy: false, dirty: false};
            this._pendingLevels.set(channel, pending);
        }
        pending.value = Math.max(0, Math.min(100, absolute ? value : pending.value + value));
        pending.dirty = true;
        const flush = () => {
            if (!this._enabled || this._pendingLevels.get(channel) !== pending || pending.busy || !pending.dirty) return;
            pending.dirty = false;
            pending.busy = true;
            this._callDBus(channel === 'mic' ? 'SetMicVolumeAbsolute' : 'SetVolumeAbsolute', [pending.value], () => {
                pending.busy = false;
                if (this._pendingLevels.get(channel) !== pending) return;
                if (pending.dirty) flush();
                else this._pendingLevels.delete(channel);
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
            this._unregisterKeybindings();
        }

        if (this._status) {
            const isAKG = this._status.active_destination.id === 'akg';
            const destShort = isAKG ? 'AKG' : 'SP166';
            const modeMarker = numLockOn ? '⚡' : '';
            this._label.set_text(`${modeMarker}${destShort} ${this._status.output_volume}%`);
        }
    }

    _registerKeybindings() {
        if (this._keybindingKeys.length > 0) return;

        const mode = Shell.ActionMode.NORMAL | Shell.ActionMode.OVERVIEW;
        const noRepeat = Meta.KeyBindingFlags.IGNORE_AUTOREPEAT;
        const allowRepeat = Meta.KeyBindingFlags.NONE;

        const bindings = [
            // Volume ±2% (Repeatable)
            {name: 'shortcut-volume-up', flags: allowRepeat, handler: () => {
                this._queueLevel('out', 2, false);
            }},
            {name: 'shortcut-volume-down', flags: allowRepeat, handler: () => {
                this._queueLevel('out', -2, false);
            }},

            // Mic ±2% (Repeatable)
            {name: 'shortcut-mic-up', flags: allowRepeat, handler: () => {
                this._queueLevel('mic', 2, false);
            }},
            {name: 'shortcut-mic-down', flags: allowRepeat, handler: () => {
                this._queueLevel('mic', -2, false);
            }},

            // Mutes (Ignore Autorepeat)
            {name: 'shortcut-mute-mic', flags: noRepeat, handler: () => {
                this._callDBus('ToggleMicMute', []);
            }},
            {name: 'shortcut-mute-output', flags: noRepeat, handler: () => {
                this._callDBus('ToggleOutputMute', []);
            }},
            {name: 'shortcut-mute-panic', flags: noRepeat, handler: () => {
                this._callDBus('TogglePanic', []);
            }},

            // Destination & Reset (Ignore Autorepeat)
            {name: 'shortcut-dest-akg', flags: noRepeat, handler: () => {
                this._callDBus('SelectDestination', ['akg']);
            }},
            {name: 'shortcut-dest-sp166', flags: noRepeat, handler: () => {
                this._callDBus('SelectDestination', ['sp166']);
            }},
            {name: 'shortcut-dest-toggle', flags: noRepeat, handler: () => {
                this._callDBus('ToggleDestination', []);
            }},
            {name: 'shortcut-reset-base', flags: noRepeat, handler: () => {
                this._callDBus('ResetDefaults', []);
            }},

            // No-Op Keys (Silent consumption to prevent number typing)
            {name: 'shortcut-noop-4', flags: noRepeat, handler: () => {}},
            {name: 'shortcut-noop-6', flags: noRepeat, handler: () => {}},
            {name: 'shortcut-noop-7', flags: noRepeat, handler: () => {}},
            {name: 'shortcut-noop-8', flags: noRepeat, handler: () => {}},
            {name: 'shortcut-noop-9', flags: noRepeat, handler: () => {}},
        ];


        for (const b of bindings) {
            const action = Main.wm.addKeybinding(b.name, this._settings, b.flags, mode, b.handler);
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

    _showOSD(message, iconName) {
        try {
            Main.osdWindowManager.show(
                -1,
                Gio.ThemedIcon.new(iconName || 'audio-volume-high-symbolic'),
                message,
                null,
                null
            );
        } catch (_e) {}
    }

    _showOSDGauge(label, level, iconName) {
        try {
            const clamped = Math.max(0, Math.min(100, level));
            Main.osdWindowManager.show(
                -1,
                Gio.ThemedIcon.new(iconName || 'audio-volume-high-symbolic'),
                `${label}: ${clamped}%`,
                clamped / 100.0,
                1
            );
        } catch (_e) {}
    }
}
