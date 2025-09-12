#!/bin/bash
cd $(dirname $0)

case "$OSTYPE" in
	darwin*) #echo "It's a Mac!!" ;
		xattr -d com.apple.quarantine I.K.E.M.E.N-Go.app
		chmod +x ./I.K.E.M.E.N-Go.app/Contents/MacOS/bundle_run.sh
		./I.K.E.M.E.N-Go.app/Contents/MacOS/bundle_run.sh
	;;
	linux*)
		#export MESA_GL_VERSION_OVERRIDE=2.1
		#export MESA_GLES_VERSION_OVERRIDE=1.5
		chmod +x Ikemen_GO_Linux
		./Ikemen_GO_Linux
	;;
	*) echo "System not recognized"; exit 1 ;;
esac
