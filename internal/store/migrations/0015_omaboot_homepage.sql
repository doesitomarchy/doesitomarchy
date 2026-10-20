-- OmaBoot? Live's homepage moved to omaboot.live. EnsureSource leaves an
-- existing source as it is, so the address registered with its key is
-- updated here.
UPDATE sources SET homepage = 'https://omaboot.live' WHERE id = 'boot-live' AND homepage = 'https://doesitboot.com';
