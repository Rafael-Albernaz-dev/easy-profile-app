import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Meta from 'gi://Meta';
import Shell from 'gi://Shell';
import St from 'gi://St';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

export default class EasyProfileExtension extends Extension {
    enable() {
        this._settings = this.getSettings();
        this._keybindingKeys = [];
        this._counter = 0;

        // 1. Create top bar indicator icon / label
        this._indicator = new PanelMenu.Button(0.0, 'EasyProfile Indicator', false);
        this._box = new St.BoxLayout({style_class: 'panel-status-indicators-box'});
        this._icon = new St.Icon({
            icon_name: 'audio-volume-high-symbolic',
            style_class: 'system-status-icon',
        });
        this._label = new St.Label({
            text: 'EP',
            y_align: Clutter.ActorAlign.CENTER,
            style_class: 'system-status-label',
        });
        this._box.add_child(this._icon);
        this._box.add_child(this._label);
        this._indicator.add_child(this._box);
        Main.panel.addToStatusArea('easyprofile-indicator', this._indicator);

        // 2. Connect to NumLock state changes via Clutter Keymap
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
        } else {
            this._label.set_text('EP [NoKeymap]');
        }
    }

    disable() {
        this._unregisterKeybindings();

        if (this._keymap && this._numLockSignalId) {
            this._keymap.disconnect(this._numLockSignalId);
            this._numLockSignalId = null;
            this._keymap = null;
        }

        if (this._indicator) {
            this._indicator.destroy();
            this._indicator = null;
        }

        this._settings = null;
    }

    _syncNumLockState() {
        if (!this._keymap) return;
        const numLockOn = Boolean(this._keymap.get_num_lock_state());

        if (numLockOn) {
            this._label.set_text('EP [Audio]');
            this._icon.set_icon_name('audio-volume-high-symbolic');
            this._registerKeybindings();
        } else {
            this._label.set_text('EP [Off]');
            this._icon.set_icon_name('audio-volume-muted-symbolic');
            this._unregisterKeybindings();
        }
    }

    _registerKeybindings() {
        if (this._keybindingKeys.length > 0) return; // Already registered

        const mode = Shell.ActionMode.NORMAL | Shell.ActionMode.OVERVIEW;
        const noRepeat = Meta.KeyBindingFlags.IGNORE_AUTOREPEAT;
        const allowRepeat = Meta.KeyBindingFlags.NONE;

        const bindings = [
            // Volume & Mic (Repeatable)
            {name: 'shortcut-volume-up', flags: allowRepeat, action: 'Vol Saída +2%'},
            {name: 'shortcut-volume-down', flags: allowRepeat, action: 'Vol Saída -2%'},
            {name: 'shortcut-mic-up', flags: allowRepeat, action: 'Mic +2%'},
            {name: 'shortcut-mic-down', flags: allowRepeat, action: 'Mic -2%'},

            // Toggles (Ignore Autorepeat)
            {name: 'shortcut-mute-mic', flags: noRepeat, action: 'Toggle Mute Mic'},
            {name: 'shortcut-mute-output', flags: noRepeat, action: 'Toggle Mute Saída'},
            {name: 'shortcut-mute-panic', flags: noRepeat, action: 'Mute Total (Pânico)'},

            // Destination & Reset (Ignore Autorepeat)
            {name: 'shortcut-dest-akg', flags: noRepeat, action: 'Destino: AKG K52'},
            {name: 'shortcut-dest-sp166', flags: noRepeat, action: 'Destino: Caixas SP166'},
            {name: 'shortcut-dest-toggle', flags: noRepeat, action: 'Alternar AKG ↔ Caixas'},
            {name: 'shortcut-reset-base', flags: noRepeat, action: 'Reset Padrão Diário'},

            // No-Op Keys (Ignore Autorepeat, silent consumption)
            {name: 'shortcut-noop-4', flags: noRepeat, action: null},
            {name: 'shortcut-noop-6', flags: noRepeat, action: null},
            {name: 'shortcut-noop-7', flags: noRepeat, action: null},
            {name: 'shortcut-noop-8', flags: noRepeat, action: null},
            {name: 'shortcut-noop-9', flags: noRepeat, action: null},
        ];

        for (const b of bindings) {
            Main.wm.addKeybinding(
                b.name,
                this._settings,
                b.flags,
                mode,
                () => {
                    this._onActionTriggered(b.action);
                }
            );
            this._keybindingKeys.push(b.name);
        }
    }

    _unregisterKeybindings() {
        for (const name of this._keybindingKeys) {
            Main.wm.removeKeybinding(name);
        }
        this._keybindingKeys = [];
    }

    _onActionTriggered(actionName) {
        if (!actionName) {
            // No-Op key consumed silently
            return;
        }

        this._counter++;
        const msg = `[EP Spike #${this._counter}] ${actionName}`;

        // Show GNOME Shell native OSD
        try {
            Main.osdWindowManager.show(
                -1,
                Gio.ThemedIcon.new('audio-volume-high-symbolic'),
                msg,
                null,
                null
            );
        } catch (_e) {
            // OSD fallback if display not ready
        }
    }
}
