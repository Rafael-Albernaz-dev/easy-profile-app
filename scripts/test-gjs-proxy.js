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
let cancelSource = 0;
const pendingCall = new Gio.Cancellable();
function verifyBoost(client, done) {
 client.SetOutputBoostRemote(true, (reply,error)=> {
  if(error||reply[0]!==true){printerr(error??'Boost enable failed');loop.quit();return;}
  client.SetVolumeAbsoluteRemote(112,(level,levelError)=> {
   if(levelError||level[0]!==112){printerr(levelError??'Boost level failed');loop.quit();return;}
   client.ToggleOutputBoostRemote((off,offError)=> {
    if(offError||off[0]!==false){printerr(offError??'Boost disable failed');loop.quit();return;}
    client.GetStatusRemote(([json],statusError)=>{
     const state=JSON.parse(json);
     if(statusError||state.output_boost||state.output_volume!==100){printerr(statusError??'Boost readback failed');loop.quit();return;}
     done();
    },cancellable);
   },cancellable);
  },cancellable);
 },cancellable);
}
function verifySharing(client, done) {
    client.SetOutputSharingRemote(true, (reply, error) => {
        if (error || reply[0] !== true) { printerr(error ?? 'invalid sharing reply'); loop.quit(); return; }
        client.GetStatusRemote(([json], statusError) => {
            if (statusError || !JSON.parse(json).output_sharing) { printerr(statusError ?? 'sharing readback missing'); loop.quit(); return; }
            client.ToggleOutputSharingRemote((offReply, offError) => {
                if (offError || offReply[0] !== false) { printerr(offError ?? 'sharing did not stop'); loop.quit(); return; }
                verifyBoost(client, done);
            }, cancellable);
        }, cancellable);
    }, cancellable);
}
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
    verifySharing(client, () => client.SetMicVolumeAbsoluteRemote(86, (result, callError) => {
        if (callError || result[0] !== 86) { printerr(callError ?? 'invalid mic reply'); loop.quit(); return; }
        client.GetStatusRemote(([json], statusError) => {
            if (statusError) { printerr(statusError); loop.quit(); return; }
            if (JSON.parse(json).mic_level !== 86 || !signalSeen) {
                printerr('GJS status/signal mismatch'); loop.quit(); return;
            }
            // Fake route stabilization is deliberately slower than client cancellation.
            // Cancelling a client call does not roll back a daemon operation already accepted.
            client.SelectDestinationRemote('sp166', (_reply, pendingError) => {
                if (pendingError?.matches(Gio.io_error_quark(), Gio.IOErrorEnum.CANCELLED)) exitCode = 0;
                else printerr(pendingError ?? 'pending call was not cancelled');
                loop.quit();
            }, pendingCall);
            cancelSource = GLib.timeout_add(GLib.PRIORITY_DEFAULT, 1, () => {
                cancelSource = 0;
                pendingCall.cancel();
                return GLib.SOURCE_REMOVE;
            });
        }, cancellable);
    }, cancellable));
}, cancellable);
loop.run();
cancellable.cancel();
pendingCall.cancel();
if (cancelSource) GLib.Source.remove(cancelSource);
if (signalId) proxy.disconnectSignal(signalId);
// Remove the timeout if it has not already fired.
if (exitCode === 0) GLib.source_remove(timeout);
if (exitCode === 0) print('Shared GJS proxy: output sharing, signed-int method, pending-call cancellation and StateChanged passed.');
System.exit(exitCode);
