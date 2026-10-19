import Gio from 'gi://Gio';

export const DBusInterface = `
<node>
  <interface name="org.easyprofile.Daemon">
    <method name="GetStatus">
      <arg type="s" direction="out" name="status_json"/>
    </method>
    <method name="SetVolume">
      <arg type="i" direction="in" name="delta"/>
      <arg type="i" direction="out" name="new_volume"/>
    </method>
    <method name="SetVolumeAbsolute">
      <arg type="i" direction="in" name="percent"/>
      <arg type="i" direction="out" name="new_volume"/>
    </method>
    <method name="SetVolumeForDestination">
      <arg type="s" direction="in" name="destination"/>
      <arg type="i" direction="in" name="percent"/>
      <arg type="i" direction="out" name="new_volume"/>
    </method>
    <method name="SetMicVolume">
      <arg type="i" direction="in" name="delta"/>
      <arg type="i" direction="out" name="new_volume"/>
    </method>
    <method name="SetMicVolumeAbsolute">
      <arg type="i" direction="in" name="percent"/>
      <arg type="i" direction="out" name="new_volume"/>
    </method>
    <method name="ToggleOutputMute">
      <arg type="b" direction="out" name="muted"/>
    </method>
    <method name="ToggleMicMute">
      <arg type="b" direction="out" name="muted"/>
    </method>
    <method name="TogglePanic">
      <arg type="b" direction="out" name="panic_active"/>
    </method>
    <method name="SelectDestination">
      <arg type="s" direction="in" name="dest_name"/>
    </method>
    <method name="ToggleDestination">
      <arg type="s" direction="out" name="active_dest"/>
    </method>
    <method name="SelectPreset">
      <arg type="s" direction="in" name="preset_name"/>
    </method>
    <method name="ResetDefaults"/>
    <method name="GetRawMicStreams">
      <arg type="as" direction="out" name="streams"/>
    </method>
    <method name="MigrateRawMicStream"><arg type="s" direction="in" name="application"/></method>
    <signal name="StateChanged">
      <arg type="s" name="status_json"/>
    </signal>
  </interface>
</node>`;

export const DaemonProxy = Gio.DBusProxy.makeProxyWrapper(DBusInterface);

