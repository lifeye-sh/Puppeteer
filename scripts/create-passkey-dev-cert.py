"""Create a short-lived leaf certificate for this project's loopback HTTPS only."""
from pathlib import Path
from datetime import datetime, timedelta, timezone
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID, ExtendedKeyUsageOID

root = Path(__file__).resolve().parents[1] / 'backend/.local/passkey-tls'
root.mkdir(parents=True, exist_ok=True)
key_path, cert_path = root / 'server.key', root / 'server.crt'
if key_path.exists() or cert_path.exists():
    raise SystemExit('Certificate files already exist; refusing to overwrite.')
key = rsa.generate_private_key(public_exponent=65537, key_size=3072)
name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, 'Next Terminal backend.localhost development')])
now = datetime.now(timezone.utc)
cert = (x509.CertificateBuilder().subject_name(name).issuer_name(name)
        .public_key(key.public_key()).serial_number(x509.random_serial_number())
        .not_valid_before(now-timedelta(minutes=5)).not_valid_after(now+timedelta(days=30))
        .add_extension(x509.SubjectAlternativeName([x509.DNSName('backend.localhost')]), critical=False)
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]), critical=False)
        .add_extension(x509.KeyUsage(digital_signature=True,content_commitment=False,key_encipherment=True,
            data_encipherment=False,key_agreement=False,key_cert_sign=False,crl_sign=False,
            encipher_only=False,decipher_only=False), critical=True)
        .sign(key, hashes.SHA256()))
key_path.write_bytes(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
cert_path.write_bytes(cert.public_bytes(serialization.Encoding.PEM))
(root / 'server.cer').write_bytes(cert.public_bytes(serialization.Encoding.DER))
print('Created backend.localhost leaf certificate, CA=false, valid 30 days.')
print('SHA256:', cert.fingerprint(hashes.SHA256()).hex())
