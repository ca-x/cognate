import copy
import hashlib
import json
import pathlib
import struct
import subprocess
import tempfile

def u32(value):
    return struct.pack('>I', value)

def u64(value):
    return struct.pack('>Q', value)

def blob(value):
    if isinstance(value, str):
        value = value.encode('utf-8')
    return u32(len(value)) + value

def digest(value):
    return hashlib.sha256(value).digest()

def sequence(values, encode=blob):
    return u32(len(values)) + b''.join(encode(value) for value in values)

def optional(record, name, encode=blob):
    return u32(0) if name not in record else u32(1) + encode(record[name])

def model(value):
    return blob(value['model']) + optional(value, 'displayName') + optional(value, 'contextWindow', u64) + optional(value, 'supportsParallelToolCalls', lambda enabled: u32(int(enabled))) + optional(value, 'inputModalities', sequence) + optional(value, 'baseInstructions') + optional(value, 'reasoningLevels', sequence) + optional(value, 'defaultReasoningLevel')

def declaration(value):
    encoded = blob('cognate/native-codex-declaration/v1') + u32(1) + u32(1)
    for name in ['target', 'providerID', 'wireAPI', 'endpoint', 'model']:
        encoded += blob(value[name])
    encoded += optional(value, 'reasoningEffort')
    encoded += optional(value, 'catalog', lambda catalog: sequence(catalog['models'], model))
    return encoded + blob(value['credentialReference']) + sequence(value['requiredSlots']) + blob(value['localAuthPolicy'])

def adapter():
    return u32(1) + blob('builtin.codex') + blob('1.1.0') + blob('f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4') + sequence(['codex.provider-plan.v1'])

def local_binding(declaration_hash, value):
    encoded = blob('cognate/native-codex-local-binding/v1') + u32(1) + blob(declaration_hash)
    encoded += blob(value['nativeProfileID']) + blob(bytes.fromhex(value['nativeProfileSHA256'])) + blob(bytes.fromhex(value['templateSHA256']))
    encoded += u32(value['effectiveScope']) + u32(value['providerOwnership']) + u32(value['catalogOwnership']) + sequence(value['managedModels'])
    encoded += u32(value['authStore']) + u32(value['keyringState']) + u32(value['fileLoginState']) + blob(value['localAuthPolicy']) + u32(int(value['requiresOpenAIAuth'])) + blob(bytes.fromhex(value['witnessSHA256']))
    encoded += u32(3)
    for entry in value['baselines']:
        encoded += u32(entry['role']) + u32(entry['presence']) + blob(bytes.fromhex(entry.get('sha256', '')))
    return encoded

desired = {
    'apiVersion': 1, 'target': 'codex.config', 'providerID': 'cognate', 'wireAPI': 'responses',
    'endpoint': 'https://provider.invalid/v1', 'model': 'alpha', 'reasoningEffort': 'high',
    'catalog': {'models': [
        {'model': 'alpha', 'displayName': 'Alpha', 'contextWindow': 131072, 'supportsParallelToolCalls': False, 'inputModalities': ['image', 'text'], 'baseInstructions': 'Keep\nspaces\t', 'reasoningLevels': ['high', 'none'], 'defaultReasoningLevel': 'high'},
        {'model': 'beta'},
    ]},
    'credentialReference': 'codex.provider-key', 'requiredSlots': ['codex.provider-key'], 'localAuthPolicy': 'preserve-local-login-v1',
}
config = b'model_provider = "cognate"\nmodel = "beta"\n[model_providers.cognate]\nname = "Cognate"\nbase_url = "https://old.invalid/v1"\nwire_api = "responses"\nrequires_openai_auth = false\n'
catalog = b'{"models":[{"slug":"beta"},{"slug":"alpha"}]}\n'
binding = {
    'nativeProfileID': 'synthetic-codec-profile-v1',
    'nativeProfileSHA256': digest(b'synthetic-native-profile').hex(),
    'templateSHA256': digest(b'synthetic-catalog-template').hex(),
    'effectiveScope': 1, 'providerOwnership': 2, 'catalogOwnership': 2, 'managedModels': ['beta', 'alpha'],
    'authStore': 2, 'keyringState': 1, 'fileLoginState': 4, 'localAuthPolicy': 'preserve-local-login-v1', 'requiresOpenAIAuth': True,
    'witnessSHA256': digest(b'synthetic-D-observation').hex(),
    'baselines': [
        {'role': 1, 'presence': 2, 'sha256': digest(config).hex()},
        {'role': 2, 'presence': 3},
        {'role': 3, 'presence': 2, 'sha256': digest(catalog).hex()},
    ],
}
origin, owner = 'https://proof.example:8443', 'owner-synthetic'
operation, session, nonce = bytes([1]) * 16, bytes([5]) * 32, bytes([6]) * 32
asset, profile, prepare_id = 'A' * 26, 'profile-c2', 'prepare-synthetic'
prepared_ms, prepare_expires, intent_expires, grant_expires = 1700000000000, 1700000300000, 1700000240000, 1700000060000
authorize_challenge, consume_challenge = bytes([11]) * 32, bytes([10]) * 32

with tempfile.TemporaryDirectory(prefix='synthetic-c2-signature-', dir='/tmp') as directory:
    temporary = pathlib.Path(directory)
    private = temporary / 'synthetic.der'
    seed = digest(b'cognate codex native consumption synthetic vector v1')
    private.write_bytes(bytes.fromhex('302e020100300506032b657004220420') + seed)
    private.chmod(0o600)
    public_der = subprocess.check_output(['openssl', 'pkey', '-inform', 'DER', '-in', str(private), '-pubout', '-outform', 'DER'], stderr=subprocess.DEVNULL)
    public_key = public_der[-32:]
    key_hash = digest(public_key)
    declaration_bytes = declaration(desired)
    declaration_hash = digest(declaration_bytes)
    local_bytes = local_binding(declaration_hash, binding)
    local_hash = digest(local_bytes)
    preparation = blob(owner) + blob(origin) + blob(session) + blob(operation) + blob(asset) + u64(7) + blob('codex') + u32(1) + u32(1) + adapter() + u32(2) + u32(1) + blob(profile) + u64(9) + sequence([3], u32) + u64(prepare_expires) + blob(key_hash) + blob(prepare_id) + u64(prepared_ms) + u32(1) + blob(declaration_hash)
    preparation_bytes = blob('cognate/native-codex-preparation/v1') + preparation
    preparation_hash = digest(preparation_bytes)
    intent_bytes = blob('cognate/native-codex-intent/v1') + u32(1) + blob(preparation_hash) + blob(declaration_hash) + blob(local_hash) + u32(1) + u64(11) + sequence([3], u32) + u64(intent_expires)
    commitment = digest(intent_bytes)
    apply_bytes = blob('cognate/native-codex-apply/v1') + blob(preparation_hash) + blob(commitment) + u64(intent_expires)
    apply_hash = digest(apply_bytes)
    prefix = lambda purpose: blob('cognate/native-codex-proof/v1') + blob(purpose) + u32(1)
    transcripts = {
        'bind': blob('cognate/native-config-proof/v1') + blob('bind') + u32(1) + blob(origin) + blob(owner) + u32(1) + blob(operation) + blob(public_key) + blob(bytes([3]) * 32) + blob('challenge-synthetic') + blob(bytes([4]) * 32) + blob(session) + u64(grant_expires) + u64(prepare_expires),
        'prepare': prefix('prepare') + blob(origin) + blob(owner) + blob(session) + blob(operation) + blob(key_hash) + blob(owner) + blob(asset) + u64(7) + blob('codex') + u32(1) + adapter() + u32(2) + u32(1) + blob(profile) + u64(9) + blob(operation) + blob(nonce) + u32(1),
        'authorize': prefix('authorize') + blob(preparation_hash) + blob(authorize_challenge) + blob(prepare_id) + blob(operation) + blob(commitment) + sequence([3], u32) + u32(1) + u64(11) + blob(nonce) + u64(intent_expires) + blob(local_hash),
        'consume': prefix('consume') + blob(apply_hash) + blob(authorize_challenge) + blob('grant-synthetic') + blob(digest(bytes([7]) * 32)) + blob(consume_challenge) + u64(grant_expires) + blob(operation) + blob(commitment) + blob(bytes([12]) * 32),
        'status': prefix('status') + blob(preparation_hash) + blob(authorize_challenge) + blob(prepare_id) + blob('') + blob(operation) + blob(bytes([13]) * 32),
    }
    signed = {}
    for purpose, transcript in transcripts.items():
        message = temporary / 'message'
        signature_file = temporary / 'signature'
        public_file = temporary / 'public.der'
        message.write_bytes(transcript)
        public_file.write_bytes(public_der)
        signature = subprocess.check_output(['openssl', 'pkeyutl', '-sign', '-rawin', '-keyform', 'DER', '-inkey', str(private), '-in', str(message)], stderr=subprocess.DEVNULL)
        signature_file.write_bytes(signature)
        subprocess.run(['openssl', 'pkeyutl', '-verify', '-rawin', '-pubin', '-keyform', 'DER', '-inkey', str(public_file), '-in', str(message), '-sigfile', str(signature_file)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        signed[purpose] = {'bytes': len(transcript), 'sha256': digest(transcript).hex(), 'signature': signature.hex(), 'transcript_hex': transcript.hex()}
    altered = copy.deepcopy(desired)
    del altered['catalog']['models'][0]['supportsParallelToolCalls']
    absent_hash = digest(declaration(altered))
    altered = copy.deepcopy(desired)
    altered['catalog']['models'].reverse()
    reordered_hash = digest(declaration(altered))
    altered_binding = copy.deepcopy(binding)
    altered_binding['baselines'][1]['presence'] = 1
    missing_auth_hash = digest(local_binding(declaration_hash, altered_binding))
    assert absent_hash != declaration_hash and reordered_hash != declaration_hash and missing_auth_hash != local_hash
    result = {
        'format': 'cognate-codex-native-consumption-spec-v1-synthetic-only',
        'declaration': desired, 'local_binding': binding, 'config_hex': config.hex(), 'catalog_hex': catalog.hex(),
        'public_key': public_key.hex(),
        'hashes': {name: digest(value).hex() for name, value in [('declaration', declaration_bytes), ('local_binding', local_bytes), ('preparation', preparation_bytes), ('intent', intent_bytes), ('apply', apply_bytes)]},
        'canonical_bytes': {'declaration': len(declaration_bytes), 'local_binding': len(local_bytes), 'preparation': len(preparation_bytes), 'intent': len(intent_bytes), 'apply': len(apply_bytes)},
        'hash_negative_vectors': {'parallel_absent_declaration': absent_hash.hex(), 'catalog_reordered_declaration': reordered_hash.hex(), 'auth_missing_local_binding': missing_auth_hash.hex()},
        'vectors': signed,
    }
    compact = lambda nonce, bound: b'CX2\x00' + u32(1) + blob(preparation) + blob(nonce) + u32(int(bound)) + (blob(local_bytes) + blob(commitment) + u64(intent_expires) + u64(11) if bound else b'')
    result['compact'] = {}
    for label, compact_nonce, bound in [('unbound', nonce, False), ('bound', nonce, True), ('unbound_nonce22', bytes([34])*32, False), ('bound_nonce22', bytes([34])*32, True)]:
        encoded = compact(compact_nonce, bound)
        result['compact'][label] = {'bytes': len(encoded), 'sha256': digest(encoded).hex(), 'hex': encoded.hex()}
    result['qualification'] = 'synthetic-protocol-only-not-native-profile-or-D'
    print(json.dumps(result, indent=2))
