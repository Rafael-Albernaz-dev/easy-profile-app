// Executes the extension's actual methods against in-memory Shell/Gio actors.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
assert.equal(process.env.EASYPROFILE_TEST_MODE, '1');
let source = fs.readFileSync(path.join(__dirname, '../gnome-extension/easyprofile@gnome-shell/extension.js'), 'utf8');
source = source.replace(/^import .*;\n/gm, '').replace('export default class EasyProfileExtension extends Extension', 'class EasyProfileExtension extends Extension');
source += '\nglobalThis.AuditedExtension = EasyProfileExtension;';
let nextSource = 1;
const timers = new Map();
const bindings = new Map();
const context = {
    Extension: class {}, console,
    GLib: {PRIORITY_DEFAULT: 0, SOURCE_REMOVE: false,
        timeout_add: (_priority, _duration, callback) => { const id = nextSource++; timers.set(id, callback); return id; },
        Source: {remove: id => { assert(timers.delete(id)); }}},
    Meta: {KeyBindingFlags: {IGNORE_AUTOREPEAT: 1, NONE: 0}, KeyBindingAction: {NONE: 0}},
    Shell: {ActionMode: {NORMAL: 1, OVERVIEW: 2}},
    Main: {wm: {addKeybinding: (name, _settings, flags, _mode, handler) => { bindings.set(name, {flags, handler}); return 1; },
        removeKeybinding: name => bindings.delete(name)}},
};
vm.createContext(context); vm.runInContext(source, context);
const actor = () => ({visible: false, value: 0, text: '', set_text(value) { this.text = value; },
    set_icon_name() {}, add_style_pseudo_class() {}, remove_style_pseudo_class() {},
    menu: {removeAll() {}, addMenuItem() {}}});
const status = (id = 'akg', output = 50) => ({active_destination: {id}, output_volume: output, mic_level: 100,
    available_presets: [], raw_mic_streams: [], observed_output_mute: false});
function extension() {
    const e = new context.AuditedExtension();
    Object.assign(e, {_enabled: true, _generation: 1, _levelGeneration: 0, _pendingLevels: new Map(),
        _levelInFlight: new Set(), _retrySources: new Set(), _barriers: [], _barrierBusy: false,
        _status: status(), _targetChannel: 'out', _keybindingKeys: [], _keymap: {get_num_lock_state: () => true},
        _cancellable: {cancel() {}}, _settings: {}});
    for (const key of ['_label', '_icon', '_muteBadge', '_micMuteBadge', '_btnAKG', '_btnSP166',
        '_volSlider', '_micSlider', '_btnMuteOut', '_btnMuteMic', '_presetsSubMenu', '_warningBanner', '_rawStreamMenu']) e[key] = actor();
    const calls = [];
    const proxy = {g_name_owner: ':fake.1'};
    for (const method of ['SetVolumeForDestination', 'SetMicVolumeAbsolute', 'SelectDestination', 'ResetDefaults', 'SelectPreset']) {
        proxy[`${method}Remote`] = (...args) => {
            const cancellable = args.pop(); const callback = args.pop();
            assert.equal(cancellable, e._cancellable);
            calls.push({method, args, callback});
        };
    }
    e._proxy = proxy;
    const errors = [];
    e._showOSD = message => errors.push(message);
    e._refreshStatus = done => done?.(e._status);
    return {e, calls, errors};
}

{
    const {e, calls} = extension();
    e._queueLevel('out', 1, false); e._queueLevel('out', 1, false);
    assert.equal(e._label.text, '⚡AKG 52%');
    e._onStateChanged(JSON.stringify(status('akg', 51)));
    assert.equal(e._label.text, '⚡AKG 52%', 'late signal must not erase pending target');
    e._callDBus('SelectDestination', ['sp166']);
    assert.equal(e._pendingLevels.size, 0);
    assert.equal(calls.length, 1, 'route must wait for sent volume');
    assert.deepEqual(Array.from(calls[0].args), ['akg', 51]);
    calls[0].callback([51], null);
    assert.equal(calls[1].method, 'SelectDestination');
    e._status = status('sp166', 20); calls[1].callback([], null);
    assert.equal(calls.length, 2, 'old AKG pending value must never flush to SP166');
    e._queueLevel('out', 1, false);
    assert.deepEqual(Array.from(calls[2].args), ['sp166', 21]);
}
{
    const {e, calls} = extension();
    e._queueLevel('mic', -1, false); e._queueLevel('mic', -1, false);
    e._callDBus('ResetDefaults', []);
    e._queueLevel('mic', -1, false);
    assert.equal(calls.length, 1, 'reset gates new volume input');
    calls[0].callback([99], null);
    assert.equal(calls[1].method, 'ResetDefaults'); calls[1].callback([], null);
    assert.equal(calls.length, 2, 'pending pre-reset mic target discarded');
    assert.equal(e._pendingLevels.size, 0);
}
{
    const {e, calls, errors} = extension();
    e._queueLevel('out', 1, false);
    calls[0].callback(null, new Error('denied'));
    assert.equal(e._pendingLevels.size, 0); assert.equal(e._label.text, '⚡AKG 50%');
    assert.equal(errors.length, 1);
}
{
    const {e, calls} = extension();
    e._queueLevel('out', 1, false);
    let cancelled = false, destroyed = false;
    e._cancellable = {cancel() { cancelled = true; }};
    e._indicator = {destroy() { destroyed = true; }};
    e._refreshStatus = context.AuditedExtension.prototype._refreshStatus;
    e._proxy.GetStatusRemote = callback => callback(null, new Error('retry'));
    e._refreshStatus(); assert.equal(timers.size, 1);
    e.disable();
    assert(cancelled && destroyed); assert.equal(timers.size, 0);
    assert.equal(e._label, null);
    calls[0].callback([51], null); // Late callbacks must settle without touching actors.
}
{
    const {e} = extension(); const actions = [];
    e._callDBus = (method, args) => actions.push([method, args]);
    e._registerKeybindings(); assert.equal(bindings.size, 16);
    for (const [name, {flags}] of bindings) {
        assert.equal(flags, name === 'shortcut-volume-up' || name === 'shortcut-volume-down' ? 0 : 1);
    }
    bindings.get('shortcut-reset-app').handler(); assert.equal(actions[0][0], 'ResetDefaults');
    bindings.get('shortcut-mute-panic').handler(); assert.equal(actions[1][0], 'TogglePanic');
    bindings.get('shortcut-select-mic').handler(); assert.equal(e._targetChannel, 'mic');
    for (const name of ['shortcut-noop-7', 'shortcut-noop-8', 'shortcut-noop-9']) bindings.get(name).handler();
    assert.equal(actions.length, 2);
    e._keymap.get_num_lock_state = () => false;
    bindings.get('shortcut-mute-panic').handler(); assert.equal(actions.length, 2);
    e._unregisterKeybindings(); assert.equal(bindings.size, 0);
}
console.log('Extension regressions passed: destination/reset barriers, late signals/errors, lifecycle, NumLock and repeat flags.');
