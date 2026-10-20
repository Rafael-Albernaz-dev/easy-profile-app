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
    setToggleState(value) { this.state = value; }, label: {set_text(value) {this.text=value;}},
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
    for (const key of ['_label', '_icon', '_muteBadge', '_micMuteBadge', '_listenBadge', '_shareItem', '_boostItem', '_btnAKG', '_btnSP166',
        '_volSlider', '_micSlider', '_btnMuteOut', '_btnMuteMic', '_presetsSubMenu', '_warningBanner', '_rawStreamMenu']) e[key] = actor();
    const calls = [];
    const proxy = {g_name_owner: ':fake.1'};
    for (const method of ['SetVolumeForDestination', 'SetMicVolumeAbsolute', 'SelectDestination', 'ResetDefaults', 'SelectPreset', 'SetOutputSharing', 'SetOutputBoost', 'ToggleOutputBoost']) {
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
    bindings.get('shortcut-noop-7').handler(); assert.equal(actions[2][0], 'ToggleMicMonitoring');
    bindings.get('shortcut-select-mic').handler(); assert.equal(e._targetChannel, 'mic');
    bindings.get('shortcut-noop-8').handler(); assert.equal(actions[3][0], 'ToggleOutputSharing');
    bindings.get('shortcut-noop-9').handler(); assert.equal(actions[4][0], 'ToggleOutputBoost');
    assert.equal(actions.length, 5);
    e._status.available_presets = ['Preset Base', 'Preset 2', 'Preset 3'];
    e._status.active_preset = 'Preset 2';
    bindings.get('shortcut-preset-1').handler();
    assert.equal(actions[5][0], 'SelectPreset'); assert.deepEqual(Array.from(actions[5][1]), ['Preset Base']);
    bindings.get('shortcut-preset-2').handler();
    assert.equal(actions[6][0], 'SelectPreset'); assert.deepEqual(Array.from(actions[6][1]), ['Preset Base']);
    bindings.get('shortcut-preset-3').handler();
    assert.equal(actions[7][0], 'SelectPreset'); assert.deepEqual(Array.from(actions[7][1]), ['Preset 3']);
    assert.equal(actions.length, 8);
    e._keymap.get_num_lock_state = () => false;
    bindings.get('shortcut-mute-panic').handler();
    bindings.get('shortcut-noop-8').handler(); bindings.get('shortcut-noop-9').handler();
    assert.equal(actions.length, 8);
    e._unregisterKeybindings(); assert.equal(bindings.size, 0);
}
{
    const {e, calls} = extension();
    e._setOutputSharing(true);
    assert.equal(e._shareItem.reactive, false);
    assert.equal(calls[0].method, 'SetOutputSharing');
    assert.deepEqual(Array.from(calls[0].args), [true]);
    const sharing = {...status(), output_sharing: true};
    e._onStateChanged(JSON.stringify(sharing));
    assert.equal(e._shareItem.state, true);
    assert.match(e._label.text, /COMPARTILHANDO/);
    calls[0].callback([true], null);
    assert.equal(e._shareItem.reactive, true);
    e._setOutputSharing(false);
    e._generation++;
    e._shareItem.reactive = false;
    calls[1].callback(null, new Error('obsolete'));
    assert.equal(e._shareItem.reactive, false, 'late call must not modify new actors');
    e._onStateChanged(JSON.stringify({...status(), output_sharing_restore_pending:true}));
    assert.equal(e._shareItem.state, true, 'pending restoration must allow retrying OFF');
    assert.match(e._label.text, /RESTAURAR ENTRADA/);
}
console.log('Extension regressions passed: sharing/readback/lifecycle, destination/reset barriers, late signals/errors, NumLock and repeat flags.');

{
 const {e,calls}=extension();
 e._status={...status('akg',100),output_boost:true,output_max_percent:112};
 e._queueLevel('out',120,true);
 assert.deepEqual(Array.from(calls[0].args),['akg',112]);
 assert.match(e._label.text,/112%.*BOOST/);
 e._queueLevel('mic',112,true);
 assert.deepEqual(Array.from(calls[1].args),[100]);
 e._callDBus('SetOutputBoost',[false]);
 assert.equal(e._pendingLevels.size,0);
 assert.equal(calls.length,2,'Boost toggle must wait for in-flight levels');
 calls[0].callback([112],null); calls[1].callback([100],null);
 assert.equal(calls[2].method,'SetOutputBoost');
 e._status={...status('akg',100),output_boost:false,output_max_percent:100};
 calls[2].callback([false],null);
 e._queueLevel('out',1,false);
 assert.deepEqual(Array.from(calls[3].args),['akg',100]);
}
