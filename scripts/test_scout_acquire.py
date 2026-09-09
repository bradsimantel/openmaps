import bz2
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location('scout_acquire', Path(__file__).with_name('scout-acquire.py'))
scout = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scout)


class ManifestTest(unittest.TestCase):
    def inputs(self, root, conflict=False, directory_missing=False):
        catalog = json.dumps({'global': {'valhalla': 'valhalla-34'},
                              'test/region': {'valhalla': {'packages': ['1'], 'timestamp': 'pinned', 'version': '2'}}}).encode()
        (root / 'catalog.json').write_bytes(catalog)
        lines = [hashlib.md5(catalog).hexdigest() + ' date countries_provided.json']
        for pid in ['1', '2']:
            lines.append('a' * 32 + ' date ' + scout.PREFIX + pid + '.tar.bz2')
        lines.append(('b' if conflict else 'a') * 32 + ' date ' + scout.PREFIX + '1.tar.bz2')
        (root / 'digest.md5.bz2').write_bytes(bz2.compress('\n'.join(lines).encode()))
        (root / 'packages.html').write_text('<a href="1.tar.bz2">one</a>' + ('' if directory_missing else '<a href="2.tar.bz2">two</a>'))

    def test_full_digest_includes_catalog_omission(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.inputs(root)
            _, packages, _ = scout.manifests(root)
            self.assertEqual(packages, {'1', '2'})

    def test_conflict_or_missing_directory_rejected(self):
        for kwargs in [dict(conflict=True), dict(directory_missing=True)]:
            with self.subTest(**kwargs), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                self.inputs(root, **kwargs)
                with self.assertRaises(ValueError):
                    scout.manifests(root)

    def test_catalog_changes_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.inputs(root)
            (root / 'catalog.json').write_text('{}')
            with self.assertRaises(ValueError):
                scout.manifests(root)

    def test_metadata_read_is_bounded(self):
        from io import BytesIO
        with patch.object(scout.urllib.request, 'urlopen', return_value=BytesIO(b'12345')):
            with self.assertRaises(ValueError):
                scout.read_url('unused', 4)

    def test_resumed_payload_must_match_sha_pin(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.inputs(root)
            (root / 'packages').mkdir()
            payload = b'retained package'
            (root / 'packages/1.tar.bz2').write_bytes(payload)
            pins = {n: scout.digest((root / n).read_bytes()) for n in ['catalog.json', 'digest.md5.bz2', 'packages.html']}
            package = dict(id='1', bytes=len(payload), md5=hashlib.md5(payload).hexdigest(), sha256='0'*64, url=scout.BASE+scout.PREFIX+'1.tar.bz2')
            plan = dict(metadata_sha256=pins, packages=[package], budgets=dict(download_bytes=scout.GIB, disk_reserve_bytes=32*scout.GIB))
            (root / 'acquisition.json').write_text(json.dumps(plan))
            def remote(url, limit):
                name = 'catalog.json' if url.endswith('countries_provided.json') else 'digest.md5.bz2'
                return (root / name).read_bytes()
            args = SimpleNamespace(root=root, plan='acquisition.json', download_gib=12, reserve_gib=32)
            with patch.object(scout, 'read_url', side_effect=remote):
                with self.assertRaisesRegex(ValueError, 'retained package checksum mismatch'):
                    scout.fetch(args)
            self.assertFalse((root / 'receipts/1.json').exists())
            self.assertEqual((root / 'packages/1.tar.bz2').read_bytes(), payload)
            plan['budgets']['download_bytes'] = 13*scout.GIB
            (root / 'acquisition.json').write_text(json.dumps(plan))
            with patch.object(scout, 'read_url', side_effect=AssertionError('must reject before network')):
                with self.assertRaisesRegex(ValueError, 'acquisition budgets'):
                    scout.fetch(args)


if __name__ == '__main__':
    unittest.main()
