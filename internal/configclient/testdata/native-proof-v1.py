import hashlib
import json
import pathlib
import struct
import subprocess
import tempfile


def u32(value):
    return struct.pack(">I", value)


def u64(value):
    return struct.pack(">Q", value)


def blob(value):
    if isinstance(value, str):
        value = value.encode("utf-8")
    return u32(len(value)) + value


def slots(values):
    return u32(len(values)) + b"".join(u32(value) for value in values)


def adapter():
    return u32(1) + blob("builtin.claude") + blob("1.1.0") + blob("a" * 64) + u32(1) + blob("claude.provider-switch.v1")


origin = "https://proof.example:8443"
owner = "owner-synthetic"
operation = bytes([1]) * 16
session = bytes([5]) * 32
asset = "A" * 26
profile = "synthetic-profile"
prepare_id = "prepare-synthetic"
authorize_challenge = bytes([11]) * 32
commitment = bytes([7]) * 32
intent_expiry = 1700000240000
patch = (
    blob("cognate/native-config-patch/v1") + u32(1) + u32(7)
    + u32(1) + u32(2) + u32(1)
    + u32(2) + u32(3)
    + u32(3) + u32(1) + blob("https://provider.invalid/v1")
    + u32(4) + u32(1) + blob("model-default")
    + u32(5) + u32(3)
    + u32(6) + u32(1) + blob("model-sonnet")
    + u32(7) + u32(3)
)
patch_hash = hashlib.sha256(patch).digest()

with tempfile.TemporaryDirectory(prefix="cognate-native-vector-") as directory:
    root = pathlib.Path(directory)
    seed = hashlib.sha256(b"cognate native proof synthetic known-answer v1").digest()
    key = root / "key.der"
    key.write_bytes(bytes.fromhex("302e020100300506032b657004220420") + seed)
    public_der = subprocess.check_output([
        "openssl", "pkey", "-inform", "DER", "-in", str(key), "-pubout", "-outform", "DER"
    ], stderr=subprocess.PIPE)
    public = public_der[-32:]
    key_hash = hashlib.sha256(public).digest()
    preparation = (
        blob(owner) + blob(origin) + blob(session) + blob(operation)
        + blob(asset) + u64(7) + blob("claude") + u32(1) + u32(1) + adapter()
        + u32(1) + u32(1) + blob(profile) + u64(9) + slots([1])
        + u64(1700000300000) + blob(key_hash) + blob(prepare_id)
        + u64(1700000000000) + blob(patch_hash)
    )
    preparation_hash = hashlib.sha256(blob("cognate/native-config-preparation/v1") + preparation).digest()
    apply_hash = hashlib.sha256(
        blob("cognate/native-config-apply/v1") + blob(preparation) + blob(commitment) + u64(intent_expiry)
    ).digest()
    fields = {
        "bind": (
            blob(origin) + blob(owner) + u32(1) + blob(operation) + blob(public)
            + blob(bytes([3]) * 32) + blob("challenge-synthetic") + blob(bytes([4]) * 32)
            + blob(session) + u64(1700000060000) + u64(1700000900000)
        ),
        "prepare": (
            blob(origin) + blob(owner) + blob(session) + blob(operation) + blob(key_hash)
            + blob(owner) + blob(asset) + u64(7) + blob("claude") + u32(1) + adapter()
            + u32(1) + u32(1) + blob(profile) + u64(9) + blob(operation) + blob(bytes([6]) * 32)
        ),
        "authorize": (
            blob(preparation_hash) + blob(authorize_challenge) + blob(prepare_id) + blob(operation)
            + blob(commitment) + slots([1]) + u32(1) + u64(11) + blob(bytes([6]) * 32) + u64(intent_expiry)
        ),
        "authorize-auto": (
            blob(preparation_hash) + blob(authorize_challenge) + blob(prepare_id) + blob(operation)
            + blob(commitment) + slots([1]) + u32(2) + blob(b"P" * 32) + u64(13)
            + blob(bytes([6]) * 32) + u64(intent_expiry)
        ),
        "consume": (
            blob(apply_hash) + blob(authorize_challenge) + blob("grant-synthetic")
            + blob(hashlib.sha256(bytes([9]) * 32).digest()) + blob(bytes([10]) * 32)
            + u64(1700000060000) + blob(operation) + blob(commitment) + blob(bytes([12]) * 32)
        ),
        "status": (
            blob(preparation_hash) + blob(authorize_challenge) + blob(prepare_id)
            + blob("") + blob(operation) + blob(bytes([13]) * 32)
        ),
    }
    result = {
        "format": "cognate-native-proof-known-answer-v1",
        "hashes": {"patch": patch_hash.hex(), "preparation": preparation_hash.hex(), "apply": apply_hash.hex()},
        "vectors": {},
    }
    for name, value in fields.items():
        purpose = "authorize" if name == "authorize-auto" else name
        transcript = blob("cognate/native-config-proof/v1") + blob(purpose) + u32(1) + value
        message = root / "message.bin"
        message.write_bytes(transcript)
        signature = subprocess.check_output([
            "openssl", "pkeyutl", "-sign", "-rawin", "-keyform", "DER", "-inkey", str(key), "-in", str(message)
        ], stderr=subprocess.PIPE)
        result["vectors"][name] = {"transcript": transcript.hex(), "signature": signature.hex()}
    print(json.dumps(result, indent=2, sort_keys=True))
