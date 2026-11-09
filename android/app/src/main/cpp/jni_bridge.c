#include <jni.h>

extern int OmnirouterStart(char *data_dir, char *gateway_key, char *encryption_key);
extern int OmnirouterStop(void);

JNIEXPORT jint JNICALL
Java_com_qingjian0_omnirouter_NativeBridge_nativeStart(
    JNIEnv *env, jobject thiz, jstring data_dir, jstring gateway_key, jstring encryption_key
) {
    (void) thiz;
    const char *data = (*env)->GetStringUTFChars(env, data_dir, 0);
    const char *gateway = (*env)->GetStringUTFChars(env, gateway_key, 0);
    const char *encryption = (*env)->GetStringUTFChars(env, encryption_key, 0);
    if (data == 0 || gateway == 0 || encryption == 0) {
        if (data != 0) (*env)->ReleaseStringUTFChars(env, data_dir, data);
        if (gateway != 0) (*env)->ReleaseStringUTFChars(env, gateway_key, gateway);
        if (encryption != 0) (*env)->ReleaseStringUTFChars(env, encryption_key, encryption);
        return -10;
    }
    int result = OmnirouterStart((char *) data, (char *) gateway, (char *) encryption);
    (*env)->ReleaseStringUTFChars(env, data_dir, data);
    (*env)->ReleaseStringUTFChars(env, gateway_key, gateway);
    (*env)->ReleaseStringUTFChars(env, encryption_key, encryption);
    return (jint) result;
}

JNIEXPORT jint JNICALL
Java_com_qingjian0_omnirouter_NativeBridge_nativeStop(JNIEnv *env, jobject thiz) {
    (void) env;
    (void) thiz;
    return (jint) OmnirouterStop();
}
