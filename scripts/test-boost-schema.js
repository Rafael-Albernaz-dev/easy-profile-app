#!/usr/bin/env gjs
// Independent proof of EasyEffects limiter GSettings contract.
// Tests parameter types, ranges and enum constraints directly against the
// installed GLib schema without writing to dconf (pure in-memory introspection).

const { Gio, GLib } = imports.gi;

const schemaSource = Gio.SettingsSchemaSource.get_default();
if (!schemaSource) {
    printerr('GSettings schema source unavailable');
    imports.system.exit(1);
}

const schema = schemaSource.lookup('com.github.wwmm.easyeffects.limiter', true);
if (!schema) {
    printerr('Schema com.github.wwmm.easyeffects.limiter not found');
    imports.system.exit(1);
}

// 1. Verify schema keys and their valid ranges/types for all boost levels (100% - 150%)
for (let pct = 100; pct <= 150; pct++) {
    const gainDb = 60 * Math.log10(pct / 100);
    const validParams = {
        'bypass': new GLib.Variant('b', false),
        'input-gain': new GLib.Variant('d', gainDb),
        'output-gain': new GLib.Variant('d', 0.0),
        'threshold': new GLib.Variant('d', 0.0),
        'gain-boost': new GLib.Variant('b', false),
        'external-sidechain': new GLib.Variant('b', false),
        'mode': new GLib.Variant('s', 'Herm Thin'),
        'oversampling': new GLib.Variant('s', 'None'),
        'dithering': new GLib.Variant('s', 'None'),
        'lookahead': new GLib.Variant('d', 5.0),
        'attack': new GLib.Variant('d', 1.0),
        'release': new GLib.Variant('d', 20.0), // Schema default is 5.0 ms (range: [0.25, 20.0])
        'stereo-link': new GLib.Variant('d', 100.0),
        'alr': new GLib.Variant('b', false),
    };

    for (const [keyName, variant] of Object.entries(validParams)) {
        if (!schema.has_key(keyName)) {
            printerr(`Schema missing expected key: ${keyName}`);
            imports.system.exit(1);
        }
        const key = schema.get_key(keyName);
        if (!key.range_check(variant)) {
            printerr(`Key ${keyName} rejected valid variant: ${variant.print(true)} for ${pct}%`);
            imports.system.exit(1);
        }
    }
}

// 2. Verify that invalid values are correctly rejected by range_check
const invalidCases = [
    { key: 'release', variant: new GLib.Variant('d', 50.0), reason: '50.0 is out of range [0.25, 20.0]' },
    { key: 'release', variant: new GLib.Variant('d', 0.1), reason: '0.1 is below min 0.25' },
    { key: 'release', variant: new GLib.Variant('d', 25.0), reason: '25.0 is above max 20.0' },
    { key: 'input-gain', variant: new GLib.Variant('d', 40.0), reason: '40.0 is above max 36.0' },
    { key: 'threshold', variant: new GLib.Variant('d', 5.0), reason: '5.0 is above max 0.0' },
    { key: 'mode', variant: new GLib.Variant('s', 'Unknown Mode'), reason: 'Unknown Mode is not in enum' },
    { key: 'oversampling', variant: new GLib.Variant('s', 'Invalid Rate'), reason: 'Invalid Rate is not in enum' },
];

for (const { key, variant, reason } of invalidCases) {
    const k = schema.get_key(key);
    if (k.range_check(variant)) {
        printerr(`Security flaw: schema accepted invalid variant: ${key}=${variant.print(true)} (${reason})`);
        imports.system.exit(1);
    }
}

print('✓ Independent GSettings schema contract test passed (zero dconf mutations).');
