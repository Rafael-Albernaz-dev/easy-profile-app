import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import System from 'system';
import {DaemonProxy} from '../gnome-extension/easyprofile@gnome-shell/dbus.js';

// Exercises the exact proxy module used by the extension, on the harness's private bus.
if (GLib.getenv('EASYPROFILE_TEST_MODE') !== '1' || GLib.getenv('EASYPROFILE_PRIVATE_BUS') !== '1')
    throw new Error('GJS integration requires private fake daemon');
const loop = new GLib.MainLoop(null, false);
const cancellable = new Gio.Cancellable();
let exitCode = 1;
let signalId = 0;
let signalSeen = false;
const timeout = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, 10, () => {
    printerr('GJS proxy timed out');
    cancellable.cancel();
    loop.quit();
    return GLib.SOURCE_REMOVE;
});
const proxy = new DaemonProxy(Gio.DBus.session, 'org.easyprofile.Daemon', '/org/easyprofile/Daemon', (client, error) => {
    if (error) { printerr(error); loop.quit(); return; }
    signalId = client.connectSignal('StateChanged', (_client, _sender, [json]) => {
        const state = JSON.parse(json);
        if (state.mic_level === 86) signalSeen = true;
    });
    client.SetMicVolumeAbsoluteRemote(86, (result, callError) => {
        if (callError || result[0] !== 86) { printerr(callError ?? 'invalid mic reply'); loop.quit(); return; }
        client.GetStatusRemote(([json], statusError) => {
            if (statusError) { printerr(statusError); loop.quit(); return; }
            if (JSON.parse(json).mic_level === 86 && signalSeen) exitCode = 0;
            else printerr('GJS status/signal mismatch');
            loop.quit();
        }, cancellable);
    }, cancellable);
}, cancellable);
loop.run();
cancellable.cancel();
if (signalId) proxy.disconnectSignal(signalId);
// Remove the timeout if it has not already fired.
if (exitCode === 0) GLib.source_remove(timeout);
if (exitCode === 0) print('Shared GJS proxy: signed-int method, cancellable and StateChanged passed.');
System.exit(exitCode);
