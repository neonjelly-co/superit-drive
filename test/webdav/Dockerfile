# The stand-in Drive storage for tests and local development: Apache with stock mod_dav, the software a Hetzner
# Storage Box runs. Everything under / is one WebDAV tree behind Basic auth, and a recursive listing (Depth:
# infinity) is refused, as on a Storage Box. Data lives inside the container and is gone when it stops.
#
# The Debian-based image, not Alpine: Alpine's apr-util keeps mod_dav_fs's lock database in gdbm, which cannot be
# opened by two requests at once, so parallel writes fail with 500. Debian's uses Berkeley DB and copes.
FROM httpd:2.4.66

RUN sed -i \
      -e 's/^#LoadModule dav_module/LoadModule dav_module/' \
      -e 's/^#LoadModule dav_fs_module/LoadModule dav_fs_module/' \
      /usr/local/apache2/conf/httpd.conf \
 && echo 'Include conf/extra/storage.conf' >> /usr/local/apache2/conf/httpd.conf \
 && mkdir -p /var/storage/files /var/storage/lock \
 && chown -R www-data:www-data /var/storage \
 && htpasswd -bc /usr/local/apache2/conf/storage.passwd drive drive-test-password

COPY storage.conf /usr/local/apache2/conf/extra/storage.conf
